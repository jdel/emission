package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jdel/emission/internal/auth"
	"github.com/jdel/emission/internal/seeder"
	"github.com/jdel/emission/internal/web"
	"github.com/rs/zerolog/log"
)

// server holds the dependencies shared by the HTTP handlers.
type server struct {
	mgr         *seeder.Manager
	torrentsDir string // where uploaded .torrent files are written

	auth             *auth.Service // nil when authentication is disabled
	publicURL        string        // externally reachable base URL, for invite links
	secure           bool          // serving over HTTPS — sets the Secure cookie flag
	wsOriginPatterns []string      // accepted Origin host patterns for /api/ws
}

// Options configures the API/UI HTTP server. When TLSCert and TLSKey are
// both set, the server runs over HTTPS.
type Options struct {
	Addr           string
	TorrentsDir    string
	WithUI         bool
	TLSCert        string // empty = plain HTTP
	TLSKey         string
	Auth           *auth.Service // nil = authentication disabled
	PublicURL      string
	TrustedProxies []string // CIDRs whose X-Forwarded-For is trusted for rate limiting
}

// Start starts the API (and optionally UI) HTTP server in a background
// goroutine and returns a function that shuts it down. A listen failure calls
// cancel to bring the rest of the program down.
func Start(cancel context.CancelFunc, mgr *seeder.Manager, opts Options) func() {
	srv := &server{
		mgr:              mgr,
		torrentsDir:      opts.TorrentsDir,
		auth:             opts.Auth,
		publicURL:        opts.PublicURL,
		secure:           opts.TLSCert != "" || (opts.Auth != nil && strings.HasPrefix(opts.PublicURL, "https://")),
		wsOriginPatterns: wsOrigins(opts.PublicURL, opts.Auth != nil),
	}
	rl := newRpsLimiter(newProxyTrust(opts.TrustedProxies))
	httpSrv := &http.Server{
		Addr:              opts.Addr,
		Handler:           logRequests(recoverPanic(srv.requireAuth(newMux(srv, opts.WithUI, rl)))),
		ReadHeaderTimeout: 10 * time.Second,
	}
	scheme := "http"
	if opts.TLSCert != "" {
		scheme = "https"
	}
	mode := []string{"api"}
	if opts.WithUI {
		mode = append(mode, "ui")
	}
	if opts.Auth != nil {
		mode = append(mode, "auth")
	}
	go func() {
		log.Info().Str("addr", opts.Addr).Str("scheme", scheme).Strs("mode", mode).Msg("HTTP listening")
		if opts.WithUI {
			browseURL := fmt.Sprintf("%s://localhost%s", scheme, opts.Addr)
			if opts.Auth != nil && opts.PublicURL != "" {
				browseURL = opts.PublicURL
			}
			log.Info().Str("url", browseURL).Msg("open this in your browser to use emission")
		}
		var err error
		if opts.TLSCert != "" {
			err = httpSrv.ListenAndServeTLS(opts.TLSCert, opts.TLSKey)
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
func newMux(srv *server, withUI bool, rl *rpsLimiter) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/torrents", srv.listTorrents)
	mux.HandleFunc("POST /api/torrents", srv.uploadTorrent)
	mux.HandleFunc("GET /api/torrents/{id}/stats", srv.torrentStats)
	mux.HandleFunc("PATCH /api/torrents/{id}", srv.updateTorrent)
	mux.HandleFunc("DELETE /api/torrents/{id}", srv.removeTorrent)
	mux.HandleFunc("GET /api/ws", srv.handleWS)

	// The caller's own upload bandwidth ceiling (works with auth off too, where
	// the owner is the root "" bucket).
	mux.HandleFunc("GET /api/bandwidth", srv.getBandwidth)
	mux.HandleFunc("PUT /api/bandwidth", srv.setMyBandwidth)

	// The caller's own tracker proxy (works with auth off too).
	mux.HandleFunc("GET /api/proxy", srv.getProxy)
	mux.HandleFunc("PUT /api/proxy", srv.setProxy)

	// Auth: status is always reachable so the UI knows whether to show login.
	// The rest of the auth routes only exist when authentication is enabled.
	mux.HandleFunc("GET /api/auth/status", srv.authStatus)
	if srv.auth != nil {
		mux.Handle("POST /api/auth/register/begin", rl.wrap(http.HandlerFunc(srv.authRegisterBegin)))
		mux.Handle("POST /api/auth/register/finish", rl.wrap(http.HandlerFunc(srv.authRegisterFinish)))
		mux.Handle("POST /api/auth/login/begin", rl.wrap(http.HandlerFunc(srv.authLoginBegin)))
		mux.Handle("POST /api/auth/login/finish", rl.wrap(http.HandlerFunc(srv.authLoginFinish)))
		mux.Handle("POST /api/auth/invite", rl.wrap(http.HandlerFunc(srv.authInvite)))
		mux.HandleFunc("POST /api/auth/logout", srv.authLogout)
		// Self-service device management (any authenticated user).
		mux.HandleFunc("GET /api/auth/me", srv.authMe)
		mux.HandleFunc("DELETE /api/auth/me/devices/{id}", srv.authRemoveMyDevice)
		mux.HandleFunc("DELETE /api/auth/me", srv.authDeleteMe)
		// Admin-only device/user management (each handler checks isAdmin).
		mux.HandleFunc("GET /api/auth/users", srv.authUsers)
		mux.HandleFunc("DELETE /api/auth/credentials/{id}", srv.authRemoveCredential)
		mux.HandleFunc("DELETE /api/auth/users/{username}", srv.authRemoveUser)
		mux.HandleFunc("PUT /api/auth/users/{username}/bandwidth", srv.setUserBandwidth)
		mux.HandleFunc("GET /api/auth/users/{username}/proxy", srv.getUserProxyAdmin)
		mux.HandleFunc("PUT /api/auth/users/{username}/proxy", srv.setUserProxyAdmin)
		// Pending invite management (admin only).
		mux.HandleFunc("GET /api/auth/invites", srv.authListInvites)
		mux.HandleFunc("DELETE /api/auth/invites/{token}", srv.authRevokeInvite)
		// Short invite redirect: /r/{token} → /?invite={token}
		mux.HandleFunc("GET /r/{token}", srv.inviteRedirect)
	}

	swaggerHandlers(mux, srv.secure)

	if withUI {
		if ui, ok := web.Handler(); ok {
			// On a fresh install (auth on, no users yet), nudge the operator to
			// the bootstrap page. /start itself only serves the SPA while the
			// bootstrap window is still open — afterwards it 302s home, so the
			// route doesn't advertise its purpose to regular users.
			mux.HandleFunc("GET /{$}", srv.serveRoot(ui))
			mux.HandleFunc("GET /start", srv.serveStart(ui))
			mux.Handle("/", ui)
		} else {
			log.Warn().Msg("--http.ui set but UI not embedded in this binary; serving API only")
		}
	}
	return mux
}
