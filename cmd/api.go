package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jdel/emission/internal/auth"
	"github.com/jdel/emission/internal/seeder"
	"github.com/jdel/emission/internal/web"
	"github.com/jdel/emission/internal/torrent"
	"github.com/jdel/emission/internal/units"
	"github.com/rs/zerolog/log"
)

// maxUploadBytes caps a multipart torrent upload. .torrent files are small;
// this is generous headroom.
const maxUploadBytes = 8 << 20

// defaultPageLimit is the page size used when ?limit is absent.
const defaultPageLimit = 10

// maxQueryLen bounds the search string accepted from clients.
const maxQueryLen = 200

// infoHashRe matches a valid torrent ID — a 40-character hex SHA-1 digest.
var infoHashRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// server holds the dependencies shared by the HTTP handlers.
type server struct {
	mgr         *seeder.Manager
	torrentsDir string // where uploaded .torrent files are written
	defMin      uint64
	defMax      uint64

	auth             *auth.Service // nil when authentication is disabled
	publicURL        string        // externally reachable base URL, for invite links
	secure           bool          // serving over HTTPS — sets the Secure cookie flag
	wsOriginPatterns []string      // accepted Origin host patterns for /api/ws
}

// httpOptions configures the API/UI HTTP server. When tlsCert and tlsKey are
// both set, the server runs over HTTPS.
type httpOptions struct {
	addr           string
	torrentsDir    string
	withUI         bool
	defMin         uint64
	defMax         uint64
	tlsCert        string // empty = plain HTTP
	tlsKey         string
	auth           *auth.Service // nil = authentication disabled
	publicURL      string
	trustedProxies []string // CIDRs whose X-Forwarded-For is trusted for rate limiting
}

// startHTTP starts the API (and optionally UI) HTTP server in a background
// goroutine and returns a function that shuts it down. A listen failure calls
// cancel to bring the rest of the program down.
func startHTTP(cancel context.CancelFunc, mgr *seeder.Manager, opts httpOptions) func() {
	srv := &server{
		mgr:              mgr,
		torrentsDir:      opts.torrentsDir,
		defMin:           opts.defMin,
		defMax:           opts.defMax,
		auth:             opts.auth,
		publicURL:        opts.publicURL,
		secure:           opts.tlsCert != "" || strings.HasPrefix(opts.publicURL, "https://"),
		wsOriginPatterns: wsOrigins(opts.publicURL),
	}
	pt := newProxyTrust(opts.trustedProxies)
	perIP := newRateLimiter(1, 10)   // ~1 req/s sustained, burst 10, per client
	global := newRateLimiter(20, 100) // backstop: ≤20/s overall on auth routes
	httpSrv := &http.Server{
		Addr:              opts.addr,
		Handler:           logRequests(recoverPanic(limitAuth(pt, perIP, global, srv.requireAuth(newMux(srv, opts.withUI))))),
		ReadHeaderTimeout: 10 * time.Second,
	}
	scheme := "http"
	if opts.tlsCert != "" {
		scheme = "https"
	}
	mode := []string{"api"}
	if opts.withUI {
		mode = append(mode, "ui")
	}
	if opts.auth != nil {
		mode = append(mode, "auth")
	}
	go func() {
		log.Info().Str("addr", opts.addr).Str("scheme", scheme).Strs("mode", mode).Msg("HTTP listening")
		if opts.withUI {
			url := opts.publicURL
			if url == "" {
				url = fmt.Sprintf("%s://localhost%s", scheme, opts.addr)
			}
			log.Info().Str("url", url).Msg("open this in your browser to use emission")
		}
		var err error
		if opts.tlsCert != "" {
			err = httpSrv.ListenAndServeTLS(opts.tlsCert, opts.tlsKey)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("HTTP server error")
			cancel()
		}
	}()
	return func() {
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = httpSrv.Shutdown(ctx)
	}
}

// newMux wires the API routes and, when withUI is set and the UI was built
// into the binary, the embedded web interface at the root.
func newMux(srv *server, withUI bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/torrents", srv.listTorrents)
	mux.HandleFunc("POST /api/torrents", srv.uploadTorrent)
	mux.HandleFunc("GET /api/torrents/{id}/stats", srv.torrentStats)
	mux.HandleFunc("PATCH /api/torrents/{id}", srv.updateTorrent)
	mux.HandleFunc("DELETE /api/torrents/{id}", srv.removeTorrent)
	mux.HandleFunc("GET /api/ws", srv.handleWS)

	// Auth: status is always reachable so the UI knows whether to show login.
	// The rest of the auth routes only exist when authentication is enabled.
	mux.HandleFunc("GET /api/auth/status", srv.authStatus)
	if srv.auth != nil {
		mux.HandleFunc("POST /api/auth/register/begin", srv.authRegisterBegin)
		mux.HandleFunc("POST /api/auth/register/finish", srv.authRegisterFinish)
		mux.HandleFunc("POST /api/auth/login/begin", srv.authLoginBegin)
		mux.HandleFunc("POST /api/auth/login/finish", srv.authLoginFinish)
		mux.HandleFunc("POST /api/auth/invite", srv.authInvite)
		mux.HandleFunc("POST /api/auth/logout", srv.authLogout)
		// Self-service device management (any authenticated user).
		mux.HandleFunc("GET /api/auth/me", srv.authMe)
		mux.HandleFunc("DELETE /api/auth/me/devices/{id}", srv.authRemoveMyDevice)
		mux.HandleFunc("DELETE /api/auth/me", srv.authDeleteMe)
		// Admin-only device/user management (each handler checks isAdmin).
		mux.HandleFunc("GET /api/auth/users", srv.authUsers)
		mux.HandleFunc("DELETE /api/auth/credentials/{id}", srv.authRemoveCredential)
		mux.HandleFunc("DELETE /api/auth/users/{username}", srv.authRemoveUser)
		// Pending invite management (admin only).
		mux.HandleFunc("GET /api/auth/invites", srv.authListInvites)
		mux.HandleFunc("DELETE /api/auth/invites/{token}", srv.authRevokeInvite)
		// Short invite redirect: /r/{token} → /?invite={token}
		mux.HandleFunc("GET /r/{token}", srv.inviteRedirect)
	}

	swaggerHandlers(mux, srv.secure)

	if withUI {
		if ui, ok := web.Handler(); ok {
			mux.Handle("/", ui)
		} else {
			log.Warn().Msg("--http.ui set but UI not embedded in this binary; serving API only")
		}
	}
	return mux
}

// listTorrents returns a filtered, paginated page of torrents.
// Query params: limit (default 10, 0 = all), offset (default 0), q (name filter).
//
//	@Summary	List torrents
//	@Tags		torrents
//	@Produce	json
//	@Param		limit	query	int		false	"Page size (0 = all, default 10)"
//	@Param		offset	query	int		false	"Page offset"
//	@Param		q		query	string	false	"Case-insensitive name filter"
//	@Success	200	{object}	pagedTorrents
//	@Router		/api/torrents [get]
func (s *server) listTorrents(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", defaultPageLimit)
	if limit < 0 {
		limit = 0
	}
	offset := queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	q := r.URL.Query().Get("q")
	if len(q) > maxQueryLen {
		q = q[:maxQueryLen]
	}
	items, total := s.mgr.Page(offset, limit, q, s.viewer(r))
	writeJSON(w, http.StatusOK, pagedTorrents{Items: items, Total: total})
}

// uploadTorrent validates an uploaded .torrent and writes it into the watched
// directory; the directory watcher then picks it up and seeds it. Per-upload
// min/max speeds (optional form fields) override the server defaults.
//
//	@Summary	Upload a .torrent file
//	@Tags		torrents
//	@Accept		multipart/form-data
//	@Produce	json
//	@Param		file		formData	file	true	"Torrent file (.torrent)"
//	@Param		min-speed	formData	string	false	"Minimum upload rate (e.g. 200K)"
//	@Param		max-speed	formData	string	false	"Maximum upload rate (e.g. 1M)"
//	@Param		max-ratio	formData	number	false	"Stop uploading at N × torrent size (0 = unlimited)"
//	@Success	202	{object}	uploadResult
//	@Failure	400	{object}	errorResponse
//	@Failure	409	{object}	errorResponse
//	@Router		/api/torrents [post]
func (s *server) uploadTorrent(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing form field \"file\"")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "cannot read uploaded file")
		return
	}

	// Validate the torrent before touching the filesystem.
	meta, err := torrent.Parse(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "not a valid .torrent: "+err.Error())
		return
	}
	id := fmt.Sprintf("%x", meta.InfoHash)
	if s.mgr.Exists(id) {
		writeError(w, http.StatusConflict, "torrent already loaded: "+meta.Name)
		return
	}

	// Resolve a safe target path. A regular user's torrents go in their own
	// subdirectory; the admin's (and auth-disabled) go in the storage root —
	// that is exactly what viewer() returns.
	target, err := s.safeTargetPath(s.uploader(r), header.Filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, statErr := os.Stat(target); statErr == nil {
		writeError(w, http.StatusConflict, "a file named "+filepath.Base(target)+" already exists")
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create storage directory")
		return
	}

	// Per-upload overrides are optional. When supplied, persist them in a
	// state file next to the .torrent so the watcher's AddFile picks them
	// up — and they survive a restart.
	min, max, ratio, override, err := parseSpeedForm(r, s.defMin, s.defMax)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if override {
		if err := seeder.SaveStateFile(target, min, max, ratio, time.Now().UnixMilli(), 0, false); err != nil {
			writeError(w, http.StatusInternalServerError, "could not save state file")
			return
		}
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save torrent file")
		return
	}
	writeJSON(w, http.StatusAccepted, uploadResult{ID: id, Name: meta.Name})
}

// parseSpeedForm pulls optional min-speed / max-speed / max-ratio values from
// a multipart form, falling back to defMin/defMax (ratio defaults to 0 =
// unlimited). override is true when at least one of the three was explicitly
// provided by the client.
func parseSpeedForm(r *http.Request, defMin, defMax uint64) (min, max uint64, ratio float64, override bool, err error) {
	min, max = defMin, defMax
	if v := r.FormValue("min-speed"); v != "" {
		if min, err = units.ParseRate(v); err != nil {
			return 0, 0, 0, false, fmt.Errorf("min-speed: %w", err)
		}
		override = true
	}
	if v := r.FormValue("max-speed"); v != "" {
		if max, err = units.ParseRate(v); err != nil {
			return 0, 0, 0, false, fmt.Errorf("max-speed: %w", err)
		}
		override = true
	}
	if v := r.FormValue("max-ratio"); v != "" {
		if ratio, err = strconv.ParseFloat(v, 64); err != nil {
			return 0, 0, 0, false, fmt.Errorf("max-ratio: %w", err)
		}
		if ratio < 0 {
			return 0, 0, 0, false, fmt.Errorf("max-ratio must be non-negative")
		}
		override = true
	}
	if min > max {
		return 0, 0, 0, false, fmt.Errorf("min-speed exceeds max-speed")
	}
	return min, max, ratio, override, nil
}

// updateTorrent edits a live torrent's min/max upload rate. JSON body:
// {"minSpeed": "200K", "maxSpeed": "2M"}. Only the owner (or admin) may change
// a torrent; the change is persisted to a state file next to the .torrent.
//
//	@Summary	Update torrent upload speed
//	@Tags		torrents
//	@Accept		json
//	@Param		id		path	string		true	"Torrent info hash (40-char hex)"
//	@Param		body	body	speedUpdate	true	"Speed settings"
//	@Success	204
//	@Failure	400	{object}	errorResponse
//	@Failure	404	{object}	errorResponse
//	@Router		/api/torrents/{id} [patch]
func (s *server) updateTorrent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !infoHashRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid torrent id")
		return
	}
	if !s.authorizeTorrent(w, r, id) {
		return
	}
	var body speedUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	min, err := units.ParseRate(body.MinSpeed)
	if err != nil {
		writeError(w, http.StatusBadRequest, "min-speed: "+err.Error())
		return
	}
	max, err := units.ParseRate(body.MaxSpeed)
	if err != nil {
		writeError(w, http.StatusBadRequest, "max-speed: "+err.Error())
		return
	}
	if err := s.mgr.SetClientOptions(id, min, max, body.MaxRatio, body.DeleteOnCap); err != nil {
		if errors.Is(err, seeder.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// torrentStats returns the in-memory stats history for one torrent as a JSON
// array of {t, r, l} objects (unix ms, rate bytes/sec, leecher count).
//
//	@Summary	Get torrent stats history
//	@Tags		torrents
//	@Produce	json
//	@Param		id	path	string	true	"Torrent info hash (40-char hex)"
//	@Success	200	{array}		seeder.StatsPoint
//	@Failure	400	{object}	errorResponse
//	@Failure	404	{object}	errorResponse
//	@Router		/api/torrents/{id}/stats [get]
func (s *server) torrentStats(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !infoHashRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid torrent id")
		return
	}
	if !s.authorizeTorrent(w, r, id) {
		return
	}
	pts, ok := s.mgr.GetStats(id)
	if !ok {
		writeError(w, http.StatusNotFound, "torrent not found")
		return
	}
	if pts == nil {
		pts = []seeder.StatsPoint{}
	}
	writeJSON(w, http.StatusOK, pts)
}

// removeTorrent stops seeding a torrent and removes it from disk.
//
//	@Summary	Remove a torrent
//	@Tags		torrents
//	@Param		id	path	string	true	"Torrent info hash (40-char hex)"
//	@Success	204
//	@Failure	404	{object}	errorResponse
//	@Router		/api/torrents/{id} [delete]
func (s *server) removeTorrent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !infoHashRe.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalid torrent id")
		return
	}
	if !s.authorizeTorrent(w, r, id) {
		return
	}
	if err := s.mgr.Remove(id); err != nil {
		if errors.Is(err, seeder.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// safeTargetPath sanitizes a client-supplied filename and returns the absolute
// path it may be written to. With a username it sits in that user's
// subdirectory; otherwise directly in the storage root. It defends against
// path traversal ("../", absolute paths, separators) in both inputs.
func (s *server) safeTargetPath(username, name string) (string, error) {
	// Drop any directory component the client may have included.
	name = filepath.Base(filepath.FromSlash(name))
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("invalid filename")
	}
	// Reject any residual separators (paranoia after Base).
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, os.PathSeparator) {
		return "", fmt.Errorf("invalid filename")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".torrent") {
		name += ".torrent"
	}
	root, err := filepath.Abs(s.torrentsDir)
	if err != nil {
		return "", fmt.Errorf("server misconfigured")
	}
	// The username comes from a registered session, but re-check it here: it
	// becomes a path segment, so it must be letters-only.
	dir := root
	if username != "" {
		if !usernameRe.MatchString(username) {
			return "", fmt.Errorf("invalid username")
		}
		dir = filepath.Join(root, username)
	}
	target := filepath.Join(dir, name)
	// Final guard: the resolved path must stay directly inside dir.
	if filepath.Dir(target) != dir {
		return "", fmt.Errorf("invalid filename")
	}
	return target, nil
}

// handleWS streams live updates to a client: a "stats" snapshot once per
// second, and a "changed" frame whenever the torrent list is added to or
// removed from. The list itself (paging, search) stays on the REST endpoint.
//
//	@Summary	WebSocket live stats feed
//	@Tags		realtime
//	@Description	Upgrades to WebSocket. Pushes {type:"stats",torrents:[...]} ~1/s and {type:"changed"} on list changes.
//	@Router		/api/ws [get]
func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Origin restriction: when --http.public-url is set we know the canonical
	// host browsers will connect from, so we lock the WebSocket to that host
	// (plus localhost for dev). When unset, the deployment is assumed
	// local-network-only, so we keep the wildcard.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.wsOriginPatterns,
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	// The viewer is fixed for the life of the connection; stats are scoped to
	// the torrents this user may see (everything, for auth-off or the admin).
	viewer := s.viewer(r)

	// CloseRead drains incoming frames and returns a context cancelled when the
	// client disconnects — we never expect client-to-server messages.
	ctx := c.CloseRead(r.Context())

	changed, unsubscribe := s.mgr.Subscribe()
	defer unsubscribe()
	statUpdates, unsubStats := s.mgr.SubscribeStats()
	defer unsubStats()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Send an immediate live snapshot.
	if wsjson.Write(ctx, c, wsMessage{Type: "stats", Torrents: s.mgr.Visible(viewer)}) != nil {
		return
	}
	// Send full stats history for all visible torrents.
	if visible := s.mgr.Visible(viewer); len(visible) > 0 {
		history := make(map[string][]seeder.StatsPoint, len(visible))
		for _, t := range visible {
			if pts, ok := s.mgr.GetStats(t.ID); ok && len(pts) > 0 {
				history[t.ID] = pts
			}
		}
		if len(history) > 0 {
			if wsjson.Write(ctx, c, wsMessage{Type: "stats_history", History: history}) != nil {
				return
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if wsjson.Write(ctx, c, wsMessage{Type: "stats", Torrents: s.mgr.Visible(viewer)}) != nil {
				return
			}
		case _, ok := <-changed:
			if !ok {
				return
			}
			if wsjson.Write(ctx, c, wsMessage{Type: "changed"}) != nil {
				return
			}
		case su, ok := <-statUpdates:
			if !ok {
				return
			}
			// Visibility check: skip if this torrent isn't visible to the viewer.
			if st, exists := s.mgr.Get(su.ID); exists {
				owner := seeder.Owner(st.Location)
				if viewer != "" && owner != viewer && owner != "" {
					continue
				}
			}
			if wsjson.Write(ctx, c, wsMessage{Type: "stat_point", ID: su.ID, Point: &su.Point}) != nil {
				return
			}
		}
	}
}

// --- helpers ----------------------------------------------------------------

func queryInt(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Error().Err(err).Msg("encode response")
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// recoverPanic is a middleware that turns a handler panic into a logged stack
// trace and a 500 response, instead of a silently dropped connection.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Error().Str("method", r.Method).Str("path", r.URL.Path).Interface("panic", v).Bytes("stack", debug.Stack()).Msg("panic recovered")
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// logRequests is a middleware that logs each request's method, path and status.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		log.Info().Str("method", r.Method).Str("path", r.URL.Path).Int("status", rec.status).Dur("duration", time.Since(start).Round(time.Millisecond)).Msg("request")
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the underlying ResponseWriter so http.ResponseController can
// reach its Hijacker/Flusher — required for the WebSocket upgrade to work
// through this middleware.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// wsOrigins returns the OriginPatterns to accept on /api/ws. When publicURL is
// set we restrict to that host plus localhost variants (so the vite dev proxy
// keeps working); otherwise we fall back to the legacy wildcard, which suits
// local-network deployments where the public URL is unknown.
func wsOrigins(publicURL string) []string {
	if publicURL == "" {
		return []string{"*"}
	}
	patterns := []string{"localhost:*", "127.0.0.1:*", "[::1]:*"}
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		patterns = append(patterns, u.Host)
	}
	return patterns
}
