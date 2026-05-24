package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jdel/emission/internal/auth"
	"github.com/jdel/emission/internal/seeder"
	"github.com/rs/zerolog/log"
)

// sessionCookie is the name of the login session cookie.
const sessionCookie = "emission_session"

// authStatus reports whether authentication is enabled and whether this
// request is authenticated. It is always reachable, so the UI can decide
// whether to show a login screen.
//
//	@Summary	Authentication status
//	@Tags		auth
//	@Produce	json
//	@Success	200	{object}	authStatusResponse
//	@Router		/api/auth/status [get]
func (s *server) authStatus(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeJSON(w, http.StatusOK, authStatusResponse{AuthEnabled: false, Authenticated: true})
		return
	}
	username, authed := s.sessionUsername(r)
	writeJSON(w, http.StatusOK, authStatusResponse{
		AuthEnabled:        true,
		Authenticated:      authed,
		Username:           username,
		DeviceCount:        s.auth.CredentialCount(),
		BootstrapAvailable: s.auth.BootstrapOpen(),
	})
}

// authRegisterBegin starts a passkey registration from an invite token.
//
//	@Summary	Begin passkey registration
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		body	body	inviteBody	true	"Invite token (empty string for bootstrap)"
//	@Success	200	{object}	registerChallenge
//	@Router		/api/auth/register/begin [post]
func (s *server) authRegisterBegin(w http.ResponseWriter, r *http.Request) {
	var body inviteBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// An empty invite is the bootstrap flow (creates admin); otherwise the
	// invite token fixes the username.
	options, ceremonyID, username, err := s.auth.BeginRegistration(body.Invite)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, registerChallenge{
		CeremonyID: ceremonyID,
		Options:    options,
		Username:   username,
	})
}

// usernameRe limits usernames to 1-20 ASCII letters.
var usernameRe = regexp.MustCompile(`^[A-Za-z]{1,20}$`)

// authRegisterFinish completes registration. The WebAuthn credential is the
// request body; the ceremony id rides in the query string. The username comes
// from the invite, so the client cannot influence it here.
//
//	@Summary	Finish passkey registration
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		ceremony	query	string	true	"Ceremony ID from /begin"
//	@Success	200	{object}	authResult
//	@Router		/api/auth/register/finish [post]
func (s *server) authRegisterFinish(w http.ResponseWriter, r *http.Request) {
	sessionID, err := s.auth.FinishRegistration(r.URL.Query().Get("ceremony"), r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.setSessionCookie(w, sessionID)
	writeJSON(w, http.StatusOK, authResult{Authenticated: true})
}

// authLoginBegin starts a passkey login.
//
//	@Summary	Begin passkey login
//	@Tags		auth
//	@Produce	json
//	@Success	200	{object}	loginChallenge
//	@Router		/api/auth/login/begin [post]
func (s *server) authLoginBegin(w http.ResponseWriter, _ *http.Request) {
	options, ceremonyID, err := s.auth.BeginLogin()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, loginChallenge{CeremonyID: ceremonyID, Options: options})
}

// authLoginFinish completes login. The WebAuthn assertion is the request body;
// the ceremony id rides in the query string.
//
//	@Summary	Finish passkey login
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		ceremony	query	string	true	"Ceremony ID from /begin"
//	@Success	200	{object}	authResult
//	@Router		/api/auth/login/finish [post]
func (s *server) authLoginFinish(w http.ResponseWriter, r *http.Request) {
	sessionID, err := s.auth.FinishLogin(r.URL.Query().Get("ceremony"), r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.setSessionCookie(w, sessionID)
	writeJSON(w, http.StatusOK, authResult{Authenticated: true})
}

// authInvite mints a one-time device-registration link for a given username.
// Inviting an existing username adds another device to that user; a new
// username creates a new user. Gated by requireAuth.
//
//	@Summary	Create an invite link (admin only)
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		body	body	inviteRequest	true	"Username for the invite"
//	@Success	200	{object}	inviteResponse
//	@Router		/api/auth/invite [post]
func (s *server) authInvite(w http.ResponseWriter, r *http.Request) {
	var body inviteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !usernameRe.MatchString(body.Username) {
		writeError(w, http.StatusBadRequest, "username must be 1-20 letters (A-Z, a-z)")
		return
	}
	if body.Username == auth.AdminUsername {
		writeError(w, http.StatusBadRequest, "the admin user cannot be invited")
		return
	}
	token, err := s.auth.CreateInvite(body.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create invite")
		return
	}
	writeJSON(w, http.StatusOK, inviteResponse{
		URL:  s.publicURL + "/register?invite=" + token,
		Code: token, // the 3-word code, for reading aloud
	})
}

// authUsers lists every registered device, for the admin to manage.
//
//	@Summary	List registered devices (admin only)
//	@Tags		admin
//	@Produce	json
//	@Success	200	{array}	deviceInfo
//	@Router		/api/auth/users [get]
func (s *server) authUsers(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	creds := s.auth.Credentials()
	out := make([]deviceInfo, len(creds))
	for i, c := range creds {
		out[i] = deviceInfo{
			ID:       base64.RawURLEncoding.EncodeToString(c.Credential.ID),
			Username: c.Username,
			AddedAt:  c.AddedAt,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// authRemoveCredential deletes a single passkey. The admin's own device is
// protected and can never be removed.
//
//	@Summary	Remove a passkey (admin only)
//	@Tags		admin
//	@Param		id	path	string	true	"Base64url credential ID"
//	@Success	204
//	@Failure	404	{object}	errorResponse
//	@Router		/api/auth/credentials/{id} [delete]
func (s *server) authRemoveCredential(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	id, err := base64.RawURLEncoding.DecodeString(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid credential id")
		return
	}
	for _, c := range s.auth.Credentials() {
		if bytes.Equal(c.Credential.ID, id) && c.Username == auth.AdminUsername {
			writeError(w, http.StatusForbidden, "the admin device cannot be removed")
			return
		}
	}
	removed, err := s.auth.RemoveCredential(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not remove credential")
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "no such credential")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authRemoveUser deletes every passkey of a user and all of their torrents
// (sessions stopped, their storage subdirectory removed). The admin user is
// protected.
//
//	@Summary	Remove a user and their torrents (admin only)
//	@Tags		admin
//	@Param		username	path	string	true	"Username"
//	@Success	204
//	@Failure	404	{object}	errorResponse
//	@Router		/api/auth/users/{username} [delete]
func (s *server) authRemoveUser(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	username := r.PathValue("username")
	if username == auth.AdminUsername {
		writeError(w, http.StatusForbidden, "the admin user cannot be removed")
		return
	}
	if !usernameRe.MatchString(username) {
		writeError(w, http.StatusBadRequest, "invalid username")
		return
	}
	n, err := s.auth.RemoveUser(username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not remove user")
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "no such user")
		return
	}
	// Stop the user's torrents and delete their storage subdirectory.
	dir := filepath.Join(s.torrentsDir, username)
	s.mgr.RemoveUnder(dir)
	if err := os.RemoveAll(dir); err != nil {
		log.Error().Err(err).Str("path", dir).Msg("could not delete user storage")
	}
	w.WriteHeader(http.StatusNoContent)
}

// authLogout ends the current session.
//
//	@Summary	Log out
//	@Tags		auth
//	@Success	204
//	@Router		/api/auth/logout [post]
func (s *server) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.auth.EndSession(c.Value)
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// sessionUsername returns the username of the request's session. ok is false
// when auth is disabled or the request has no valid session.
func (s *server) sessionUsername(r *http.Request) (username string, ok bool) {
	if s.auth == nil {
		return "", false
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	return s.auth.SessionUser(c.Value)
}

// authenticated reports whether the request may use the API. When auth is
// disabled it always returns true; otherwise it needs a valid session.
func (s *server) authenticated(r *http.Request) bool {
	if s.auth == nil {
		return true
	}
	_, ok := s.sessionUsername(r)
	return ok
}

// isAdmin reports whether the request is authenticated as the admin user.
func (s *server) isAdmin(r *http.Request) bool {
	username, ok := s.sessionUsername(r)
	return ok && username == auth.AdminUsername
}

// authorizeTorrent verifies that the request may act on the torrent with id.
// Returns ok=true when the caller is auth-disabled, the admin, or the owner.
// When ok=false the handler should have already written the response: a
// 404 if the torrent does not exist, a 403 if it does but isn't theirs.
func (s *server) authorizeTorrent(w http.ResponseWriter, r *http.Request, id string) bool {
	if s.auth == nil || s.isAdmin(r) {
		return true
	}
	viewer, _ := s.sessionUsername(r)
	st, ok := s.mgr.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "no such torrent: "+id)
		return false
	}
	if seeder.Owner(st.Location) != viewer {
		writeError(w, http.StatusForbidden, "not your torrent")
		return false
	}
	return true
}

// viewer returns the storage scope for a request: the empty string for the
// admin or when auth is disabled (sees every torrent, uploads to the storage
// root), otherwise the caller's own username (sees and uploads to their
// subdirectory).
func (s *server) viewer(r *http.Request) string {
	username, ok := s.sessionUsername(r)
	if !ok || username == auth.AdminUsername {
		return ""
	}
	return username
}

func (s *server) setSessionCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
	})
}

func (s *server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// publicAPIPaths are the API routes reachable without a session — the ones
// needed to check status and to log in or register. Everything else under
// /api/ (including /api/auth/invite and /api/auth/logout) needs a session.
var publicAPIPaths = map[string]bool{
	"/api/auth/status":          true,
	"/api/auth/login/begin":     true,
	"/api/auth/login/finish":    true,
	"/api/auth/register/begin":  true,
	"/api/auth/register/finish": true,
}

// requireAuth gates the API. Static UI assets and the public auth routes pass
// through unauthenticated (the SPA must load to show a login screen); every
// other /api/* request needs a valid session. A no-op when auth is disabled.
//
// Note: /docs/* is intentionally reachable without auth. The OpenAPI spec
// describes endpoint shapes but contains no secrets, and the docs UI must
// be browsable for API consumers integrating with an auth-protected server.
func (s *server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil ||
			!strings.HasPrefix(r.URL.Path, "/api/") ||
			publicAPIPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if !s.authenticated(r) {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
