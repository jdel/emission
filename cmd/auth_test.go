package cmd

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jdel/emission/internal/auth"
	"github.com/jdel/emission/internal/client"
	"github.com/jdel/emission/internal/seeder"
)

const authTestPublicURL = "https://emission.example:8443"

func newAuthServer(t *testing.T) (*server, *auth.Service) {
	t.Helper()
	creds, err := auth.LoadCredentials(filepath.Join(t.TempDir(), "k.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.NewService(authTestPublicURL, creds)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	mgr := seeder.New(c, t.TempDir(), 0, false)
	t.Cleanup(mgr.Shutdown)
	srv := &server{
		mgr:         mgr,
		torrentsDir: t.TempDir(),
		auth:        svc,
		publicURL:   authTestPublicURL,
	}
	return srv, svc
}

// cookieFor seeds a credential and session for username; returns the cookie.
func cookieFor(t *testing.T, svc *auth.Service, username string) *http.Cookie {
	t.Helper()
	if err := svc.SeedCredential(webauthn.Credential{ID: []byte(username + "-d1")}, username, ""); err != nil {
		t.Fatal(err)
	}
	id, err := svc.SeedSession(username)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: id}
}

func credID(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// ── GET /api/auth/me ──────────────────────────────────────────────────────────

func TestAuthMeUnauthorized(t *testing.T) {
	srv, _ := newAuthServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	w := httptest.NewRecorder()
	srv.authMe(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthMeReturnsOwnDevices(t *testing.T) {
	srv, svc := newAuthServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	r.AddCookie(cookieFor(t, svc, "alice"))
	w := httptest.NewRecorder()
	srv.authMe(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var out []deviceInfo
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Username != "alice" {
		t.Errorf("devices = %+v, want [{alice}]", out)
	}
}

func TestAuthMeDoesNotLeakOtherUsers(t *testing.T) {
	srv, svc := newAuthServer(t)
	// alice has a session; bob also has a credential.
	aliceCookie := cookieFor(t, svc, "alice")
	if err := svc.SeedCredential(webauthn.Credential{ID: []byte("bob-d1")}, "bob", ""); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	r.AddCookie(aliceCookie)
	w := httptest.NewRecorder()
	srv.authMe(w, r)

	var out []deviceInfo
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	for _, d := range out {
		if d.Username != "alice" {
			t.Errorf("response contains device for %q (not alice)", d.Username)
		}
	}
}

// ── DELETE /api/auth/me/devices/{id} ─────────────────────────────────────────

func TestAuthRemoveMyDeviceUnauthorized(t *testing.T) {
	srv, _ := newAuthServer(t)
	r := httptest.NewRequest(http.MethodDelete, "/api/auth/me/devices/abc", nil)
	r.SetPathValue("id", "abc")
	w := httptest.NewRecorder()
	srv.authRemoveMyDevice(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthRemoveMyDeviceNotFound(t *testing.T) {
	srv, svc := newAuthServer(t)
	r := httptest.NewRequest(http.MethodDelete, "/", nil)
	r.AddCookie(cookieFor(t, svc, "alice"))
	r.SetPathValue("id", credID([]byte("no-such-device")))
	w := httptest.NewRecorder()
	srv.authRemoveMyDevice(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestAuthRemoveMyDeviceCannotRemoveOtherUser(t *testing.T) {
	srv, svc := newAuthServer(t)
	aliceCookie := cookieFor(t, svc, "alice")
	if err := svc.SeedCredential(webauthn.Credential{ID: []byte("bob-d1")}, "bob", ""); err != nil {
		t.Fatal(err)
	}
	// Alice tries to delete bob's credential.
	r := httptest.NewRequest(http.MethodDelete, "/", nil)
	r.AddCookie(aliceCookie)
	r.SetPathValue("id", credID([]byte("bob-d1")))
	w := httptest.NewRecorder()
	srv.authRemoveMyDevice(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("cross-user removal: status = %d, want 404", w.Code)
	}
}

func TestAuthRemoveMyDeviceSuccess(t *testing.T) {
	srv, svc := newAuthServer(t)
	// Give alice two devices so the last-device path isn't triggered.
	if err := svc.SeedCredential(webauthn.Credential{ID: []byte("alice-d1")}, "alice", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.SeedCredential(webauthn.Credential{ID: []byte("alice-d2")}, "alice", ""); err != nil {
		t.Fatal(err)
	}
	id, err := svc.SeedSession("alice")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: id}

	r := httptest.NewRequest(http.MethodDelete, "/", nil)
	r.AddCookie(cookie)
	r.SetPathValue("id", credID([]byte("alice-d1")))
	w := httptest.NewRecorder()
	srv.authRemoveMyDevice(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
	// alice-d1 must be gone; alice-d2 must survive.
	found := map[string]bool{}
	for _, c := range svc.Credentials() {
		if c.Username == "alice" {
			found[string(c.Credential.ID)] = true
		}
	}
	if found["alice-d1"] {
		t.Error("alice-d1 still present after removal")
	}
	if !found["alice-d2"] {
		t.Error("alice-d2 was collaterally removed")
	}
}

// ── DELETE /api/auth/me ───────────────────────────────────────────────────────

func TestAuthDeleteMeUnauthorized(t *testing.T) {
	srv, _ := newAuthServer(t)
	r := httptest.NewRequest(http.MethodDelete, "/api/auth/me", nil)
	w := httptest.NewRecorder()
	srv.authDeleteMe(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthDeleteMeAdminForbidden(t *testing.T) {
	srv, svc := newAuthServer(t)
	r := httptest.NewRequest(http.MethodDelete, "/api/auth/me", nil)
	r.AddCookie(cookieFor(t, svc, auth.AdminUsername))
	w := httptest.NewRecorder()
	srv.authDeleteMe(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("admin self-delete: status = %d, want 403", w.Code)
	}
}

func TestAuthDeleteMeRemovesUser(t *testing.T) {
	srv, svc := newAuthServer(t)
	r := httptest.NewRequest(http.MethodDelete, "/api/auth/me", nil)
	r.AddCookie(cookieFor(t, svc, "alice"))
	w := httptest.NewRecorder()
	srv.authDeleteMe(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
	for _, c := range svc.Credentials() {
		if c.Username == "alice" {
			t.Error("alice credentials still present after self-delete")
		}
	}
}
