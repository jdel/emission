package auth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

func TestCredentialStoreLoadEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	s, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 0 {
		t.Errorf("fresh store count = %d, want 0", s.Count())
	}
	if len(s.WebAuthnID()) != 32 {
		t.Errorf("user id len = %d, want 32", len(s.WebAuthnID()))
	}
}

func TestCredentialStoreAddPersistReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	s, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	cred := webauthn.Credential{ID: []byte{1, 2, 3}, PublicKey: []byte{9, 8, 7}}
	if err := s.Add(cred, "Laptop"); err != nil {
		t.Fatal(err)
	}

	// Reload from disk: the credential and the user handle must survive.
	s2, err := LoadCredentials(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Count() != 1 {
		t.Fatalf("reloaded count = %d, want 1", s2.Count())
	}
	if string(s2.WebAuthnID()) != string(s.WebAuthnID()) {
		t.Error("user id not stable across reload")
	}
	if string(s2.WebAuthnCredentials()[0].ID) != string(cred.ID) {
		t.Error("credential id not preserved")
	}
}

func TestCredentialStoreUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	s, _ := LoadCredentials(path)
	cred := webauthn.Credential{ID: []byte{1}, PublicKey: []byte{1}}
	_ = s.Add(cred, "a")

	cred.PublicKey = []byte{2, 2}
	if err := s.Update(cred); err != nil {
		t.Fatal(err)
	}
	if got := s.WebAuthnCredentials()[0].PublicKey; string(got) != string([]byte{2, 2}) {
		t.Errorf("update not applied: %v", got)
	}
}

func TestCredentialStoreAddDuplicate(t *testing.T) {
	s, _ := LoadCredentials(filepath.Join(t.TempDir(), "k.json"))
	cred := webauthn.Credential{ID: []byte{1}, PublicKey: []byte{9}}
	if err := s.Add(cred, "first"); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(cred, "second"); err == nil {
		t.Error("second Add with same id should error")
	}
}

func TestCredentialStoreUsernameFor(t *testing.T) {
	s, _ := LoadCredentials(filepath.Join(t.TempDir(), "k.json"))
	_ = s.Add(webauthn.Credential{ID: []byte{1}}, "alice")
	_ = s.Add(webauthn.Credential{ID: []byte{2}}, "bob")
	if got := s.UsernameFor([]byte{1}); got != "alice" {
		t.Errorf("UsernameFor(1) = %q, want alice", got)
	}
	if got := s.UsernameFor([]byte{2}); got != "bob" {
		t.Errorf("UsernameFor(2) = %q, want bob", got)
	}
	if got := s.UsernameFor([]byte{99}); got != "" {
		t.Errorf("UsernameFor(unknown) = %q, want \"\"", got)
	}
}

func TestCredentialStoreRemove(t *testing.T) {
	s, _ := LoadCredentials(filepath.Join(t.TempDir(), "k.json"))
	_ = s.Add(webauthn.Credential{ID: []byte{1}}, "a")
	_ = s.Add(webauthn.Credential{ID: []byte{2}}, "b")

	removed, err := s.Remove([]byte{99}) // not present
	if err != nil || removed {
		t.Errorf("Remove(unknown) = (%v, %v), want (false, nil)", removed, err)
	}
	removed, err = s.Remove([]byte{1})
	if err != nil || !removed {
		t.Errorf("Remove(1) = (%v, %v), want (true, nil)", removed, err)
	}
	if s.Count() != 1 {
		t.Errorf("count after Remove = %d, want 1", s.Count())
	}
	if s.UsernameFor([]byte{1}) != "" {
		t.Error("removed credential still resolves to a username")
	}
}

func TestCredentialStoreRemoveUser(t *testing.T) {
	s, _ := LoadCredentials(filepath.Join(t.TempDir(), "k.json"))
	_ = s.Add(webauthn.Credential{ID: []byte{1}}, "alice")
	_ = s.Add(webauthn.Credential{ID: []byte{2}}, "alice") // second device
	_ = s.Add(webauthn.Credential{ID: []byte{3}}, "bob")

	n, err := s.RemoveUser("nobody")
	if err != nil || n != 0 {
		t.Errorf("RemoveUser(nobody) = (%d, %v), want (0, nil)", n, err)
	}
	n, err = s.RemoveUser("alice")
	if err != nil || n != 2 {
		t.Errorf("RemoveUser(alice) = (%d, %v), want (2, nil)", n, err)
	}
	if s.Count() != 1 {
		t.Errorf("count = %d, want 1 (bob's device)", s.Count())
	}
}

func TestCredentialStoreList(t *testing.T) {
	s, _ := LoadCredentials(filepath.Join(t.TempDir(), "k.json"))
	_ = s.Add(webauthn.Credential{ID: []byte{1}}, "alice")

	out := s.List()
	if len(out) != 1 || out[0].Username != "alice" {
		t.Fatalf("List = %+v", out)
	}
	// Mutating the returned slice must not affect the store.
	out[0].Username = "mallory"
	if got := s.List()[0].Username; got != "alice" {
		t.Errorf("store mutated via returned slice: got %q", got)
	}
}

func TestLoadCredentialsCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(path); err == nil {
		t.Error("LoadCredentials should error on malformed file")
	}
}
