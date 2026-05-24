// Package auth handles passkey (WebAuthn) authentication for the single user
// of the application: the registered credential store, login sessions, and
// one-time device-invite tokens.
package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// StoredCredential is one registered passkey plus bookkeeping. Username is a
// cosmetic label chosen at registration — every credential still belongs to
// the single WebAuthn user; there is no per-username access control.
type StoredCredential struct {
	Username   string              `json:"username"`
	AddedAt    int64               `json:"addedAt"` // unix milliseconds
	Credential webauthn.Credential `json:"credential"`
}

// CredentialStore holds the registered passkeys and persists them to a JSON
// file. It also implements webauthn.User: the application has exactly one
// user, and this store represents it.
type CredentialStore struct {
	path string

	mu     sync.Mutex
	userID []byte
	creds  []StoredCredential
}

// credentialFile is the on-disk JSON shape.
type credentialFile struct {
	UserID      []byte             `json:"userId"`
	Credentials []StoredCredential `json:"credentials"`
}

// LoadCredentials reads the passkey file at path. A missing file yields an
// empty store with a fresh random user handle (the bootstrap case).
func LoadCredentials(path string) (*CredentialStore, error) {
	s := &CredentialStore{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.userID, err = randomBytes(32)
		return s, err
	}
	if err != nil {
		return nil, err
	}
	var f credentialFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse passkey file %s: %w", path, err)
	}
	s.userID, s.creds = f.UserID, f.Credentials
	if len(s.userID) == 0 {
		if s.userID, err = randomBytes(32); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Count returns the number of registered passkeys.
func (s *CredentialStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.creds)
}

// Add stores a newly registered credential under a username label. It rejects
// a credential whose ID is already stored, so one credential maps to exactly
// one username.
func (s *CredentialStore) Add(cred webauthn.Credential, username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.creds {
		if bytes.Equal(c.Credential.ID, cred.ID) {
			return fmt.Errorf("this passkey is already registered")
		}
	}
	s.creds = append(s.creds, StoredCredential{
		Username:   username,
		AddedAt:    time.Now().UnixMilli(),
		Credential: cred,
	})
	return s.save()
}

// Update replaces a stored credential (matched by ID) with cred — used to
// persist the authenticator sign count after a successful login. A credential
// that is not found is silently ignored.
func (s *CredentialStore) Update(cred webauthn.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.creds {
		if bytes.Equal(s.creds[i].Credential.ID, cred.ID) {
			s.creds[i].Credential = cred
			return s.save()
		}
	}
	return nil
}

// save writes the store to disk atomically. The caller must hold s.mu.
func (s *CredentialStore) save() error {
	data, err := json.MarshalIndent(
		credentialFile{UserID: s.userID, Credentials: s.creds}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// --- webauthn.User ---------------------------------------------------------

func (s *CredentialStore) WebAuthnID() []byte          { return s.userID }
func (s *CredentialStore) WebAuthnName() string        { return "emission" }
func (s *CredentialStore) WebAuthnDisplayName() string { return "emission" }

func (s *CredentialStore) WebAuthnCredentials() []webauthn.Credential {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]webauthn.Credential, len(s.creds))
	for i, c := range s.creds {
		out[i] = c.Credential
	}
	return out
}

// UsernameFor returns the username a credential was registered under, matched
// by credential ID. Returns "" if no credential matches.
func (s *CredentialStore) UsernameFor(credentialID []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.creds {
		if bytes.Equal(c.Credential.ID, credentialID) {
			return c.Username
		}
	}
	return ""
}

// List returns a copy of every stored credential, for the admin to inspect.
func (s *CredentialStore) List() []StoredCredential {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]StoredCredential, len(s.creds))
	copy(out, s.creds)
	return out
}

// Remove deletes the credential with the given ID. removed is false if no
// credential matched.
func (s *CredentialStore) Remove(credentialID []byte) (removed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.creds {
		if bytes.Equal(s.creds[i].Credential.ID, credentialID) {
			s.creds = append(s.creds[:i], s.creds[i+1:]...)
			return true, s.save()
		}
	}
	return false, nil
}

// RemoveUser deletes every credential belonging to username and returns how
// many were removed.
func (s *CredentialStore) RemoveUser(username string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.creds[:0]
	removed := 0
	for _, c := range s.creds {
		if c.Username == username {
			removed++
			continue
		}
		kept = append(kept, c)
	}
	s.creds = kept
	if removed == 0 {
		return 0, nil
	}
	return removed, s.save()
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}
