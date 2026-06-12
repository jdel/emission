// Package auth handles passkey (WebAuthn) authentication for the single user
// of the application: the registered credential store, login sessions, and
// one-time device-invite tokens.
package auth

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jdel/emission/internal/model"
	"github.com/jdel/emission/internal/storage"
	"github.com/jdel/emission/internal/storage/file"
)

// StoredCredential is one registered passkey plus bookkeeping. See
// model.StoredCredential for the field semantics.
type StoredCredential = model.StoredCredential

// CredentialStore holds the registered passkeys and persists them through the
// credential repository. It also implements webauthn.User: the application
// has exactly one user, and this store represents it.
type CredentialStore struct {
	repo storage.CredentialRepo

	mu     sync.Mutex
	userID []byte
	creds  []StoredCredential
}

// LoadCredentials reads the passkey file at path. A missing file yields an
// empty store with a fresh random user handle (the bootstrap case).
func LoadCredentials(path string) (*CredentialStore, error) {
	s := &CredentialStore{repo: file.Credentials{Path: path}}
	cs, ok, err := s.repo.Load()
	if err != nil {
		return nil, err
	}
	if ok {
		s.userID, s.creds = cs.UserID, cs.Credentials
	}
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
func (s *CredentialStore) Add(cred webauthn.Credential, username, invitedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.creds {
		if bytes.Equal(c.Credential.ID, cred.ID) {
			return fmt.Errorf("this passkey is already registered")
		}
	}
	s.creds = append(s.creds, StoredCredential{
		Username:   username,
		InvitedBy:  invitedBy,
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

// save persists the store. The caller must hold s.mu.
func (s *CredentialStore) save() error {
	return s.repo.Save(model.CredentialSet{UserID: s.userID, Credentials: s.creds})
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
