package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
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
	mgr := seeder.New(c, t.TempDir(), 0, false, 1<<30, "")
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

// TestBandwidthPerUserAndAdmin impersonates three users (via seeded session
// cookies — no passkey ceremony) to verify per-user bandwidth/profile isolation,
// admin override, and that a non-admin cannot edit someone else.
func TestBandwidthPerUserAndAdmin(t *testing.T) {
	srv, svc := newAuthServer(t)
	alice := cookieFor(t, svc, "alice")
	bob := cookieFor(t, svc, "bob")
	admin := cookieFor(t, svc, auth.AdminUsername)

	getBW := func(c *http.Cookie) bandwidthInfo {
		r := httptest.NewRequest(http.MethodGet, "/api/bandwidth", nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		srv.getBandwidth(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET bandwidth: status %d", w.Code)
		}
		var info bandwidthInfo
		if err := json.NewDecoder(w.Body).Decode(&info); err != nil {
			t.Fatal(err)
		}
		return info
	}
	putSelf := func(c *http.Cookie, body string) int {
		r := httptest.NewRequest(http.MethodPut, "/api/bandwidth", strings.NewReader(body))
		r.AddCookie(c)
		w := httptest.NewRecorder()
		srv.setMyBandwidth(w, r)
		return w.Code
	}
	putUser := func(c *http.Cookie, username, body string) int {
		r := httptest.NewRequest(http.MethodPut, "/api/auth/users/"+username+"/bandwidth", strings.NewReader(body))
		r.SetPathValue("username", username)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		srv.setUserBandwidth(w, r)
		return w.Code
	}

	// alice sets her own bandwidth + curve (stealth = halfSaturation 10).
	if code := putSelf(alice, `{"bandwidth":"2M","halfSaturation":10}`); code != http.StatusNoContent {
		t.Fatalf("alice self PUT: %d", code)
	}
	if a := getBW(alice); a.Bandwidth != 2<<20 || a.Profile != "stealth" {
		t.Errorf("alice = %+v, want 2M/stealth", a)
	}
	// bob is untouched — per-user isolation.
	if b := getBW(bob); b.Profile != "normal" {
		t.Errorf("bob profile = %q, want default normal", b.Profile)
	}
	// admin overrides bob's settings (aggressive = halfSaturation 1).
	if code := putUser(admin, "bob", `{"bandwidth":"512K","halfSaturation":1}`); code != http.StatusNoContent {
		t.Fatalf("admin set bob: %d", code)
	}
	if b := getBW(bob); b.Bandwidth != 512<<10 || b.Profile != "aggressive" {
		t.Errorf("bob = %+v, want 512K/aggressive", b)
	}
	// a non-admin cannot edit another user.
	if code := putUser(alice, "bob", `{"bandwidth":"9M"}`); code != http.StatusForbidden {
		t.Errorf("alice→bob PUT: %d, want 403", code)
	}
}

// TestAdminRoutesRejectNonAdmin confirms every admin-only handler refuses a
// regular user with 403. The admin check runs before any path/body parsing, so
// a bare request with the non-admin's cookie is enough.
func TestAdminRoutesRejectNonAdmin(t *testing.T) {
	srv, svc := newAuthServer(t)
	alice := cookieFor(t, svc, "alice")

	routes := []struct {
		name string
		h    func(http.ResponseWriter, *http.Request)
	}{
		{"list users", srv.authUsers},
		{"remove credential", srv.authRemoveCredential},
		{"remove user", srv.authRemoveUser},
		{"set user bandwidth", srv.setUserBandwidth},
		{"get user proxy", srv.getUserProxyAdmin},
		{"set user proxy", srv.setUserProxyAdmin},
		{"list invites", srv.authListInvites},
		{"revoke invite", srv.authRevokeInvite},
	}
	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.AddCookie(alice)
			w := httptest.NewRecorder()
			rt.h(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s: status %d, want 403", rt.name, w.Code)
			}
		})
	}
}

// TestUserProxyAdmin confirms an admin can read and set any user's tracker
// proxy, that the SSRF guard and username validation still apply, and that a
// non-admin is refused.
func TestUserProxyAdmin(t *testing.T) {
	srv, svc := newAuthServer(t)
	alice := cookieFor(t, svc, "alice")
	admin := cookieFor(t, svc, auth.AdminUsername)

	getUser := func(c *http.Cookie, username string) (int, proxyInfo) {
		r := httptest.NewRequest(http.MethodGet, "/api/auth/users/"+username+"/proxy", nil)
		r.SetPathValue("username", username)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		srv.getUserProxyAdmin(w, r)
		var pi proxyInfo
		_ = json.NewDecoder(w.Body).Decode(&pi)
		return w.Code, pi
	}
	setUser := func(c *http.Cookie, username, body string) (int, proxyInfo) {
		r := httptest.NewRequest(http.MethodPut, "/api/auth/users/"+username+"/proxy", strings.NewReader(body))
		r.SetPathValue("username", username)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		srv.setUserProxyAdmin(w, r)
		var pi proxyInfo
		_ = json.NewDecoder(w.Body).Decode(&pi)
		return w.Code, pi
	}

	// admin sets bob's proxy: well-formed but unreachable → saved (status error).
	if code, pi := setUser(admin, "bob", `{"proxy":"http://proxy.invalid:9"}`); code != http.StatusOK || pi.Proxy != "http://proxy.invalid:9" {
		t.Fatalf("admin set bob: code %d pi %+v", code, pi)
	}
	// admin reads bob's proxy back.
	if code, pi := getUser(admin, "bob"); code != http.StatusOK || pi.Proxy != "http://proxy.invalid:9" {
		t.Errorf("admin get bob: code %d pi %+v", code, pi)
	}
	// local/private address rejected (SSRF guard).
	if code, _ := setUser(admin, "bob", `{"proxy":"http://127.0.0.1:8080"}`); code != http.StatusBadRequest {
		t.Errorf("admin set local: %d, want 400", code)
	}
	// invalid username (digits disallowed) rejected before any mutation.
	if code, _ := setUser(admin, "b0b", `{"proxy":""}`); code != http.StatusBadRequest {
		t.Errorf("invalid username: %d, want 400", code)
	}
	// non-admin cannot edit another user.
	if code, _ := setUser(alice, "bob", `{"proxy":""}`); code != http.StatusForbidden {
		t.Errorf("alice→bob: %d, want 403", code)
	}
}

// TestSelfRouteSandbox confirms a user can only act on their own account.
func TestSelfRouteSandbox(t *testing.T) {
	srv, svc := newAuthServer(t)
	alice := cookieFor(t, svc, "alice")
	// bob owns a device alice must not be able to remove.
	if err := svc.SeedCredential(webauthn.Credential{ID: []byte("bob-d1")}, "bob", ""); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodDelete, "/api/auth/me/devices/x", nil)
	r.SetPathValue("id", credID([]byte("bob-d1")))
	r.AddCookie(alice)
	w := httptest.NewRecorder()
	srv.authRemoveMyDevice(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("alice removing bob's device: %d, want 404", w.Code)
	}

	// The admin account cannot be self-deleted.
	admin := cookieFor(t, svc, auth.AdminUsername)
	r = httptest.NewRequest(http.MethodDelete, "/api/auth/me", nil)
	r.AddCookie(admin)
	w = httptest.NewRecorder()
	srv.authDeleteMe(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("admin self-delete: %d, want 403", w.Code)
	}
}

// writeTorrent writes a minimal valid single-file .torrent into dir.
func writeTorrent(t *testing.T, dir, name, announce string) string {
	t.Helper()
	const pieces = "01234567890123456789" // exactly 20 bytes
	body := fmt.Sprintf("d8:announce%d:%s4:infod6:lengthi1024e4:name%d:%s12:piece lengthi16384e6:pieces20:%see",
		len(announce), announce, len(name), name, pieces)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name+".torrent")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// newAuthServerWithTorrents builds an auth-enabled server whose manager watches
// a real dir, backed by a quiet httptest tracker for any seeded torrents.
// Returns the server, the auth service, the watched dir, and the tracker URL.
func newAuthServerWithTorrents(t *testing.T) (*server, *auth.Service, string, string) {
	t.Helper()
	trk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("d8:completei0e10:incompletei0e8:intervali999999ee"))
	}))
	dir := t.TempDir()
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	mgr := seeder.New(c, dir, 0, false, 1<<30, "")
	t.Cleanup(trk.Close) // closed after mgr.Shutdown (LIFO) so stop-announces hit a live server
	t.Cleanup(mgr.Shutdown)
	creds, err := auth.LoadCredentials(filepath.Join(t.TempDir(), "k.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.NewService(authTestPublicURL, creds)
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{mgr: mgr, torrentsDir: dir, auth: svc, publicURL: authTestPublicURL}
	return srv, svc, dir, trk.URL
}

// TestTorrentOwnershipSandbox confirms a non-owner is blocked (403) from a
// torrent that exists, gets 404 for one that doesn't, and that the owner and
// the admin may both act on it.
func TestTorrentOwnershipSandbox(t *testing.T) {
	srv, svc, dir, trackerURL := newAuthServerWithTorrents(t)

	// A torrent owned by alice (it lives under <dir>/alice/).
	path := writeTorrent(t, filepath.Join(dir, "alice"), "movie", trackerURL)
	st, err := srv.mgr.AddFile(path)
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID

	alice := cookieFor(t, svc, "alice")
	bob := cookieFor(t, svc, "bob")
	admin := cookieFor(t, svc, auth.AdminUsername)

	patch := func(c *http.Cookie, id string) int {
		r := httptest.NewRequest(http.MethodPatch, "/api/torrents/"+id, strings.NewReader(`{"maxSpeed":"1M","maxRatio":0}`))
		r.SetPathValue("id", id)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		srv.updateTorrent(w, r)
		return w.Code
	}
	stats := func(c *http.Cookie, id string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/torrents/"+id+"/stats", nil)
		r.SetPathValue("id", id)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		srv.torrentStats(w, r)
		return w.Code
	}
	del := func(c *http.Cookie, id string) int {
		r := httptest.NewRequest(http.MethodDelete, "/api/torrents/"+id, nil)
		r.SetPathValue("id", id)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		srv.removeTorrent(w, r)
		return w.Code
	}

	const ghost = "ffffffffffffffffffffffffffffffffffffffff"
	// bob is not the owner: blocked from a torrent that exists.
	if code := patch(bob, id); code != http.StatusForbidden {
		t.Errorf("bob PATCH alice's torrent: %d, want 403", code)
	}
	if code := stats(bob, id); code != http.StatusForbidden {
		t.Errorf("bob stats alice's torrent: %d, want 403", code)
	}
	if code := del(bob, id); code != http.StatusForbidden {
		t.Errorf("bob DELETE alice's torrent: %d, want 403", code)
	}
	// A torrent that does not exist → 404 (not 403), so ownership is not leaked.
	if code := patch(bob, ghost); code != http.StatusNotFound {
		t.Errorf("bob PATCH nonexistent: %d, want 404", code)
	}
	// The owner may edit her own.
	if code := patch(alice, id); code != http.StatusNoContent {
		t.Errorf("alice PATCH own torrent: %d, want 204", code)
	}
	// The admin may act on anyone's.
	if code := del(admin, id); code != http.StatusNoContent {
		t.Errorf("admin DELETE alice's torrent: %d, want 204", code)
	}
}

// TestTorrentVisibilitySandbox confirms a listing shows a user only their own
// torrents plus root-level (shared) ones — never another user's — while the
// admin sees everything.
func TestTorrentVisibilitySandbox(t *testing.T) {
	srv, svc, dir, trackerURL := newAuthServerWithTorrents(t)

	add := func(sub, name string) {
		p := writeTorrent(t, filepath.Join(dir, sub), name, trackerURL)
		if _, err := srv.mgr.AddFile(p); err != nil {
			t.Fatal(err)
		}
	}
	add("alice", "amovie")
	add("bob", "bmovie")
	add("", "shared") // root-level: shared with everyone

	// names returns the set of torrent names a viewer's listing includes.
	names := func(c *http.Cookie) map[string]bool {
		r := httptest.NewRequest(http.MethodGet, "/api/torrents", nil)
		r.AddCookie(c)
		w := httptest.NewRecorder()
		srv.listTorrents(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("list: status %d", w.Code)
		}
		var pg pagedTorrents
		if err := json.NewDecoder(w.Body).Decode(&pg); err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, it := range pg.Items {
			out[it.Name] = true
		}
		return out
	}

	alice := names(cookieFor(t, svc, "alice"))
	if !alice["amovie"] || !alice["shared"] || alice["bmovie"] {
		t.Errorf("alice sees %v, want amovie+shared, not bmovie", alice)
	}
	bob := names(cookieFor(t, svc, "bob"))
	if !bob["bmovie"] || !bob["shared"] || bob["amovie"] {
		t.Errorf("bob sees %v, want bmovie+shared, not amovie", bob)
	}
	admin := names(cookieFor(t, svc, auth.AdminUsername))
	if !admin["amovie"] || !admin["bmovie"] || !admin["shared"] {
		t.Errorf("admin sees %v, want all three", admin)
	}
}

// TestWSVisibilitySandbox confirms the live WebSocket feed's initial snapshot is
// scoped to the connecting user: own + root torrents only, never another
// user's; the admin gets everything. Uses a real HTTP server + ws upgrade.
func TestWSVisibilitySandbox(t *testing.T) {
	srv, svc, dir, trackerURL := newAuthServerWithTorrents(t)

	add := func(sub, name string) {
		p := writeTorrent(t, filepath.Join(dir, sub), name, trackerURL)
		if _, err := srv.mgr.AddFile(p); err != nil {
			t.Fatal(err)
		}
	}
	add("alice", "amovie")
	add("bob", "bmovie")
	add("", "shared")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws", srv.handleWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws"

	// visibleNames dials the feed with one user's cookie and returns the torrent
	// names in the first ("stats") frame.
	visibleNames := func(c *http.Cookie) map[string]bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{"Cookie": []string{c.String()}},
		})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer conn.CloseNow()

		var msg wsMessage
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			t.Fatalf("read: %v", err)
		}
		if msg.Type != "stats" {
			t.Fatalf("first frame type = %q, want stats", msg.Type)
		}
		out := map[string]bool{}
		for _, tr := range msg.Torrents {
			out[tr.Name] = true
		}
		return out
	}

	alice := visibleNames(cookieFor(t, svc, "alice"))
	if !alice["amovie"] || !alice["shared"] || alice["bmovie"] {
		t.Errorf("alice WS sees %v, want amovie+shared, not bmovie", alice)
	}
	bob := visibleNames(cookieFor(t, svc, "bob"))
	if !bob["bmovie"] || !bob["shared"] || bob["amovie"] {
		t.Errorf("bob WS sees %v, want bmovie+shared, not amovie", bob)
	}
	admin := visibleNames(cookieFor(t, svc, auth.AdminUsername))
	if !admin["amovie"] || !admin["bmovie"] || !admin["shared"] {
		t.Errorf("admin WS sees %v, want all three", admin)
	}
}

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

// stubUI is a minimal http.Handler that records when it ran, used to verify
// the bootstrap routes fall through to the SPA vs. issue a redirect.
type stubUI struct{ served bool }

func (s *stubUI) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.served = true
	w.WriteHeader(http.StatusOK)
}

// ── GET / and GET /start (bootstrap routing) ─────────────────────────────────

// TestServeRootRedirectsToStartOnFreshInstall verifies that with auth on and
// no credentials, GET / sends the operator to /start.
func TestServeRootRedirectsToStartOnFreshInstall(t *testing.T) {
	srv, _ := newAuthServer(t)
	ui := &stubUI{}
	w := httptest.NewRecorder()
	srv.serveRoot(ui)(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/start" {
		t.Errorf("Location = %q, want /start", loc)
	}
	if ui.served {
		t.Error("SPA was served on /, expected redirect")
	}
}

// TestServeRootServesSPAWhenUsersExist verifies that once any user is
// registered, GET / falls through to the SPA — even during the bootstrap
// window (admin reset scenario).
func TestServeRootServesSPAWhenUsersExist(t *testing.T) {
	srv, svc := newAuthServer(t)
	if err := svc.SeedCredential(webauthn.Credential{ID: []byte("bob-d1")}, "bob", ""); err != nil {
		t.Fatal(err)
	}
	ui := &stubUI{}
	w := httptest.NewRecorder()
	srv.serveRoot(ui)(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code == http.StatusFound {
		t.Fatalf("got 302 to %q, expected SPA passthrough", w.Header().Get("Location"))
	}
	if !ui.served {
		t.Error("SPA was not served on /")
	}
}

// TestServeStartServesSPAWhileBootstrapOpen verifies that /start renders
// the bootstrap page while the window is open.
func TestServeStartServesSPAWhileBootstrapOpen(t *testing.T) {
	srv, _ := newAuthServer(t)
	ui := &stubUI{}
	w := httptest.NewRecorder()
	srv.serveStart(ui)(w, httptest.NewRequest(http.MethodGet, "/start", nil))
	if w.Code == http.StatusFound {
		t.Fatalf("got 302 to %q, expected SPA passthrough", w.Header().Get("Location"))
	}
	if !ui.served {
		t.Error("SPA was not served on /start")
	}
}

// TestServeStartRedirectsHomeWhenBootstrapClosed verifies that /start hides
// itself once the bootstrap window has closed (e.g. admin already registered).
func TestServeStartRedirectsHomeWhenBootstrapClosed(t *testing.T) {
	srv, svc := newAuthServer(t)
	if err := svc.SeedCredential(webauthn.Credential{ID: []byte("admin-d1")}, auth.AdminUsername, ""); err != nil {
		t.Fatal(err)
	}
	ui := &stubUI{}
	w := httptest.NewRecorder()
	srv.serveStart(ui)(w, httptest.NewRequest(http.MethodGet, "/start", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
	if ui.served {
		t.Error("SPA was served on /start with bootstrap closed")
	}
}

// TestServeStartRedirectsHomeWhenAuthDisabled verifies that /start does not
// expose a bootstrap page when auth is off.
func TestServeStartRedirectsHomeWhenAuthDisabled(t *testing.T) {
	srv := &server{}
	ui := &stubUI{}
	w := httptest.NewRecorder()
	srv.serveStart(ui)(w, httptest.NewRequest(http.MethodGet, "/start", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}
