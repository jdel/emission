package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jdel/emission/internal/seeder"
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
	// A torrent with no explicit override follows the uploader's bandwidth.
	max, ratio, override, err := parseSpeedForm(r, s.mgr.Bandwidth(s.uploader(r)))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if override {
		if err := seeder.SaveStateFile(target, max, ratio, time.Now().UnixMilli(), 0, false); err != nil {
			writeError(w, http.StatusInternalServerError, "could not save state file")
			return
		}
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save torrent file")
		return
	}
	res := uploadResult{ID: id, Name: meta.Name}
	if meta.TruncatedTrackers > 0 {
		res.Notice = fmt.Sprintf(
			"%d tracker URL(s) found; only the first %d were kept (emission targets private trackers).",
			meta.TruncatedTrackers+len(meta.AnnounceURLs), len(meta.AnnounceURLs))
		log.Warn().Str("torrent", meta.Name).Int("found", meta.TruncatedTrackers+len(meta.AnnounceURLs)).
			Int("kept", len(meta.AnnounceURLs)).Msg("announce list truncated")
	}
	writeJSON(w, http.StatusAccepted, res)
}

// parseSpeedForm pulls optional max-speed / max-ratio values from a multipart
// form, falling back to defaultMax when max-speed is omitted (ratio defaults to
// 0 = unlimited). override is true when at least one was explicitly provided by
// the client.
func parseSpeedForm(r *http.Request, defaultMax uint64) (max uint64, ratio float64, override bool, err error) {
	max = defaultMax
	if v := r.FormValue("max-speed"); v != "" {
		if max, err = units.ParseRate(v); err != nil {
			return 0, 0, false, fmt.Errorf("max-speed: %w", err)
		}
		override = true
	}
	if v := r.FormValue("max-ratio"); v != "" {
		if ratio, err = strconv.ParseFloat(v, 64); err != nil {
			return 0, 0, false, fmt.Errorf("max-ratio: %w", err)
		}
		if ratio < 0 {
			return 0, 0, false, fmt.Errorf("max-ratio must be non-negative")
		}
		override = true
	}
	return max, ratio, override, nil
}

// updateTorrent edits a live torrent's max upload rate and ratio cap. JSON
// body: {"maxSpeed": "2M", "maxRatio": 0}. Only the owner (or admin) may change
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
	max, err := units.ParseRate(body.MaxSpeed)
	if err != nil {
		writeError(w, http.StatusBadRequest, "max-speed: "+err.Error())
		return
	}
	if err := s.mgr.SetClientOptions(id, max, body.MaxRatio, body.DeleteOnCap); err != nil {
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
