package auth

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

const testPublicURL = "https://emission.example:8443"

func newService(t *testing.T) *Service {
	t.Helper()
	creds, err := LoadCredentials(filepath.Join(t.TempDir(), "k.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(testPublicURL, creds)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestNewServiceInvalidURL(t *testing.T) {
	creds, _ := LoadCredentials(filepath.Join(t.TempDir(), "k.json"))
	for _, u := range []string{"", "no-scheme", "://bad", "/relative/path"} {
		if _, err := NewService(u, creds); err == nil {
			t.Errorf("NewService(%q) should error", u)
		}
	}
}

func TestBootstrapOpen(t *testing.T) {
	svc := newService(t)
	if !svc.BootstrapOpen() {
		t.Fatal("fresh store with no creds should have bootstrap open")
	}
	// Past the window with no creds → still closed.
	svc.startedAt = time.Now().Add(-bootstrapWindow - time.Minute)
	if svc.BootstrapOpen() {
		t.Error("past-window service should report closed")
	}
	// Within window but creds exist → closed.
	svc.startedAt = time.Now()
	_ = svc.creds.Add(webauthn.Credential{ID: []byte{1}}, AdminUsername)
	if svc.BootstrapOpen() {
		t.Error("with creds registered, bootstrap should be closed")
	}
}

func TestBeginRegistrationBootstrap(t *testing.T) {
	svc := newService(t)
	options, ceremonyID, username, err := svc.BeginRegistration("")
	if err != nil {
		t.Fatal(err)
	}
	if username != AdminUsername {
		t.Errorf("username = %q, want %q", username, AdminUsername)
	}
	if options == nil {
		t.Error("nil options")
	}
	if ceremonyID == "" {
		t.Error("empty ceremony id")
	}
	// Ceremony must be retrievable via takeCeremony (consumed after).
	if _, ok := svc.takeCeremony(ceremonyID); !ok {
		t.Error("ceremony not stored after BeginRegistration")
	}
	if _, ok := svc.takeCeremony(ceremonyID); ok {
		t.Error("ceremony should be one-shot (already consumed)")
	}
}

func TestBeginRegistrationBootstrapClosed(t *testing.T) {
	svc := newService(t)
	svc.startedAt = time.Now().Add(-bootstrapWindow - time.Minute)
	if _, _, _, err := svc.BeginRegistration(""); err == nil {
		t.Error("BeginRegistration with closed bootstrap should error")
	}
}

func TestBeginRegistrationInvalidInvite(t *testing.T) {
	svc := newService(t)
	if _, _, _, err := svc.BeginRegistration("not-a-real-token"); err == nil {
		t.Error("bad invite should error")
	}
}

func TestBeginRegistrationGoodInvite(t *testing.T) {
	svc := newService(t)
	token, err := svc.CreateInvite("alice")
	if err != nil {
		t.Fatal(err)
	}
	_, ceremonyID, username, err := svc.BeginRegistration(token)
	if err != nil {
		t.Fatal(err)
	}
	if username != "alice" {
		t.Errorf("username = %q, want alice", username)
	}
	if ceremonyID == "" {
		t.Error("empty ceremony id")
	}
	// Begin must NOT consume the invite — only Finish does.
	if u, ok := svc.invites.Lookup(token); !ok || u != "alice" {
		t.Errorf("invite consumed prematurely (lookup=%q, ok=%v)", u, ok)
	}
}

func TestBeginRegistrationExcludesExistingCreds(t *testing.T) {
	svc := newService(t)
	_ = svc.creds.Add(webauthn.Credential{ID: []byte("cred-1")}, AdminUsername)
	// Reopen bootstrap by resetting startedAt — the credential we just added
	// would normally close it. (BootstrapOpen checks count, not startedAt
	// alone; force the empty-invite path via a fresh invite instead.)
	tok, _ := svc.CreateInvite("alice")
	options, _, _, err := svc.BeginRegistration(tok)
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Response.CredentialExcludeList) != 1 {
		t.Fatalf("exclude list size = %d, want 1", len(options.Response.CredentialExcludeList))
	}
	if string(options.Response.CredentialExcludeList[0].CredentialID) != "cred-1" {
		t.Error("exclude list does not contain the existing credential id")
	}
}

func TestFinishRegistrationUnknownCeremony(t *testing.T) {
	svc := newService(t)
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	if _, err := svc.FinishRegistration("does-not-exist", req); err == nil {
		t.Error("unknown ceremony id should error")
	}
}

func TestBeginLoginNoDevices(t *testing.T) {
	svc := newService(t)
	if _, _, err := svc.BeginLogin(); err == nil {
		t.Error("BeginLogin with zero credentials should error")
	}
}

func TestBeginLoginWithDevice(t *testing.T) {
	svc := newService(t)
	_ = svc.creds.Add(webauthn.Credential{ID: []byte("cred-1")}, "alice")
	options, ceremonyID, err := svc.BeginLogin()
	if err != nil {
		t.Fatal(err)
	}
	if options == nil {
		t.Error("nil options")
	}
	if ceremonyID == "" {
		t.Error("empty ceremony id")
	}
	if _, ok := svc.takeCeremony(ceremonyID); !ok {
		t.Error("ceremony not stored after BeginLogin")
	}
}

func TestFinishLoginUnknownCeremony(t *testing.T) {
	svc := newService(t)
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	if _, err := svc.FinishLogin("does-not-exist", req); err == nil {
		t.Error("unknown ceremony id should error")
	}
}

func TestTakeCeremonyExpired(t *testing.T) {
	svc := newService(t)
	svc.mu.Lock()
	svc.ceremonies["expired"] = ceremony{expiry: time.Now().Add(-time.Minute)}
	svc.mu.Unlock()
	if _, ok := svc.takeCeremony("expired"); ok {
		t.Error("expired ceremony should not be returned")
	}
	// And it must also be evicted from the map.
	svc.mu.Lock()
	_, present := svc.ceremonies["expired"]
	svc.mu.Unlock()
	if present {
		t.Error("expired ceremony should be evicted")
	}
}

func TestTakeCeremonyUnknown(t *testing.T) {
	svc := newService(t)
	if _, ok := svc.takeCeremony("nope"); ok {
		t.Error("unknown ceremony id should return ok=false")
	}
}

func TestSessionUserAndEndSession(t *testing.T) {
	svc := newService(t)
	id, err := svc.sessions.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := svc.SessionUser(id); !ok || u != "alice" {
		t.Errorf("SessionUser before End: (%q, %v), want (alice, true)", u, ok)
	}
	svc.EndSession(id)
	if _, ok := svc.SessionUser(id); ok {
		t.Error("SessionUser after End should be invalid")
	}
}

func TestServiceCredentialsDelegation(t *testing.T) {
	svc := newService(t)
	if svc.CredentialCount() != 0 {
		t.Errorf("fresh count = %d, want 0", svc.CredentialCount())
	}
	_ = svc.creds.Add(webauthn.Credential{ID: []byte{1}}, "alice")
	if svc.CredentialCount() != 1 {
		t.Errorf("after add, count = %d, want 1", svc.CredentialCount())
	}
	list := svc.Credentials()
	if len(list) != 1 || list[0].Username != "alice" {
		t.Errorf("Credentials() = %+v", list)
	}

	// RemoveCredential delegates and returns the underlying flag.
	removed, err := svc.RemoveCredential([]byte{99})
	if err != nil || removed {
		t.Errorf("RemoveCredential(unknown) = (%v, %v)", removed, err)
	}
	removed, err = svc.RemoveCredential([]byte{1})
	if err != nil || !removed {
		t.Errorf("RemoveCredential(1) = (%v, %v)", removed, err)
	}

	// RemoveUser likewise.
	_ = svc.creds.Add(webauthn.Credential{ID: []byte{2}}, "bob")
	n, err := svc.RemoveUser("nobody")
	if err != nil || n != 0 {
		t.Errorf("RemoveUser(nobody) = (%d, %v)", n, err)
	}
	n, err = svc.RemoveUser("bob")
	if err != nil || n != 1 {
		t.Errorf("RemoveUser(bob) = (%d, %v)", n, err)
	}
}
