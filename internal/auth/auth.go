package auth

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// AdminUsername is the fixed name of the privileged user created at bootstrap.
const AdminUsername = "admin"

// Token lifetimes and the bootstrap window.
const (
	sessionTTL      = 7 * 24 * time.Hour // sliding login session
	inviteTTL       = 24 * time.Hour     // one-time device invite
	ceremonyTTL     = 5 * time.Minute    // in-flight WebAuthn begin→finish window
	bootstrapWindow = 15 * time.Minute   // open window to create admin after start
)

// ceremony is an in-flight WebAuthn registration or login, holding the
// challenge state produced by Begin* until the matching Finish* call.
type ceremony struct {
	data   webauthn.SessionData
	invite string // invite token, set for registration ceremonies
	expiry time.Time
}

// Service ties WebAuthn to the credential, session, and invite stores. It is
// the single entry point for authentication.
type Service struct {
	wa        *webauthn.WebAuthn
	creds     *CredentialStore
	sessions  *SessionStore
	invites   *InviteStore
	startedAt time.Time // for the bootstrap window

	mu         sync.Mutex
	ceremonies map[string]ceremony
}

// NewService builds the auth service. publicURL is the externally reachable
// base URL (e.g. https://emission.example.com:8443); WebAuthn binds passkeys to
// its host, so it must match what the browser sees.
func NewService(publicURL string, creds *CredentialStore) (*Service, error) {
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid public URL %q (want e.g. https://host:port)", publicURL)
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: "emission",
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn config: %w", err)
	}
	return &Service{
		wa:         wa,
		creds:      creds,
		sessions:   NewSessionStore(sessionTTL),
		invites:    NewInviteStore(inviteTTL),
		startedAt:  time.Now(),
		ceremonies: make(map[string]ceremony),
	}, nil
}

// BootstrapOpen reports whether the time-boxed window to create the admin user
// is still open: admin has no registered credentials, and the server started
// less than bootstrapWindow ago. Other users may exist (e.g. admin was removed).
func (s *Service) BootstrapOpen() bool {
	if time.Since(s.startedAt) >= bootstrapWindow {
		return false
	}
	for _, sc := range s.creds.List() {
		if sc.Username == AdminUsername {
			return false
		}
	}
	return true
}

// CredentialCount returns how many passkeys are registered.
func (s *Service) CredentialCount() int { return s.creds.Count() }

// Credentials returns every registered credential, for the admin to manage.
func (s *Service) Credentials() []StoredCredential { return s.creds.List() }

// RemoveCredential deletes one passkey by credential ID.
func (s *Service) RemoveCredential(credentialID []byte) (removed bool, err error) {
	return s.creds.Remove(credentialID)
}

// RemoveUser deletes every passkey of a username and returns the count removed.
func (s *Service) RemoveUser(username string) (int, error) {
	return s.creds.RemoveUser(username)
}

// CreateInvite mints a one-time device-registration token enrolling username.
// createdBy is the username of whoever is creating the invite (stored for the
// admin tree view). An empty username makes a bootstrap invite.
func (s *Service) CreateInvite(username, createdBy string) (string, error) {
	return s.invites.Create(username, createdBy)
}

// ListInvites returns all non-expired pending invite tokens.
func (s *Service) ListInvites() []PendingInvite { return s.invites.List() }

// RevokeInvite deletes a pending invite. Returns false if not found.
func (s *Service) RevokeInvite(token string) bool { return s.invites.Revoke(token) }

// SessionUser returns the username of a live login session (and slides it).
// ok is false for an unknown or expired session.
func (s *Service) SessionUser(id string) (username string, ok bool) {
	return s.sessions.Lookup(id)
}

// EndSession ends a login session (logout).
func (s *Service) EndSession(id string) { s.sessions.Delete(id) }

// SeedCredential adds a credential directly, bypassing the WebAuthn ceremony.
// Intended for tests and administrative import — not exposed via the HTTP API.
func (s *Service) SeedCredential(cred webauthn.Credential, username, invitedBy string) error {
	return s.creds.Add(cred, username, invitedBy)
}

// SeedSession creates a session for username without a WebAuthn ceremony.
// Intended for tests that need a valid session cookie.
func (s *Service) SeedSession(username string) (string, error) {
	return s.sessions.Create(username)
}

// BeginRegistration starts a passkey registration. An empty invite means the
// bootstrap flow — allowed only while the bootstrap window is open — and
// enrols the admin. Otherwise the invite must be valid; it is consumed only
// when FinishRegistration succeeds. Returns the ceremony id and the username
// being enrolled.
func (s *Service) BeginRegistration(invite string) (options *protocol.CredentialCreation, ceremonyID, username string, err error) {
	if invite == "" {
		if !s.BootstrapOpen() {
			return nil, "", "", fmt.Errorf("bootstrap window is closed")
		}
		username = AdminUsername
	} else {
		var ok bool
		if username, ok = s.invites.Lookup(invite); !ok {
			return nil, "", "", fmt.Errorf("invalid or expired invite")
		}
	}
	// Exclude credentials already registered for this username so the same
	// device cannot enrol twice. Scoped to the username so the list stays well
	// below the browser's 64-item limit regardless of total user count.
	var exclude []protocol.CredentialDescriptor
	for _, sc := range s.creds.List() {
		if sc.Username == username {
			exclude = append(exclude, protocol.CredentialDescriptor{
				Type:         protocol.PublicKeyCredentialType,
				CredentialID: sc.Credential.ID,
			})
		}
	}
	options, sessionData, err := s.wa.BeginRegistration(s.creds,
		webauthn.WithExclusions(exclude),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
	)
	if err != nil {
		return nil, "", "", err
	}
	ceremonyID, err = s.putCeremony(*sessionData, invite)
	if err != nil {
		return nil, "", "", err
	}
	return options, ceremonyID, username, nil
}

// FinishRegistration completes registration: resolves the username (admin for
// a bootstrap ceremony, otherwise the invite's, which it consumes), verifies
// the authenticator response, stores the credential, and returns a session.
func (s *Service) FinishRegistration(ceremonyID string, r *http.Request) (string, error) {
	c, ok := s.takeCeremony(ceremonyID)
	if !ok {
		return "", fmt.Errorf("unknown or expired registration")
	}
	var username, invitedBy string
	if c.invite == "" {
		if !s.BootstrapOpen() {
			return "", fmt.Errorf("bootstrap window is closed")
		}
		username = AdminUsername
	} else {
		username, invitedBy, ok = s.invites.Consume(c.invite)
		if !ok {
			return "", fmt.Errorf("invalid or expired invite")
		}
	}
	cred, err := s.wa.FinishRegistration(s.creds, c.data, r)
	if err != nil {
		return "", err
	}
	if err := s.creds.Add(*cred, username, invitedBy); err != nil {
		return "", err
	}
	return s.sessions.Create(username)
}

// BeginLogin starts a passkey login. The returned ceremony id must be handed
// back to FinishLogin.
func (s *Service) BeginLogin() (*protocol.CredentialAssertion, string, error) {
	if s.creds.Count() == 0 {
		return nil, "", fmt.Errorf("no devices registered")
	}
	options, sessionData, err := s.wa.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationPreferred),
	)
	if err != nil {
		return nil, "", err
	}
	id, err := s.putCeremony(*sessionData, "")
	if err != nil {
		return nil, "", err
	}
	return options, id, nil
}

// FinishLogin completes login and returns a fresh session id.
func (s *Service) FinishLogin(ceremonyID string, r *http.Request) (string, error) {
	c, ok := s.takeCeremony(ceremonyID)
	if !ok {
		return "", fmt.Errorf("unknown or expired login")
	}
	handler := func(rawID, _ []byte) (webauthn.User, error) { return s.creds, nil }
	cred, err := s.wa.FinishDiscoverableLogin(handler, c.data, r)
	if err != nil {
		return "", err
	}
	_ = s.creds.Update(*cred) // persist the authenticator sign count
	// The username is whichever passkey was used to authenticate.
	return s.sessions.Create(s.creds.UsernameFor(cred.ID))
}

// putCeremony stores in-flight WebAuthn state and returns its lookup id.
func (s *Service) putCeremony(data webauthn.SessionData, invite string) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	now := time.Now()
	s.mu.Lock()
	for k, c := range s.ceremonies { // drop expired/abandoned ceremonies
		if now.After(c.expiry) {
			delete(s.ceremonies, k)
		}
	}
	s.ceremonies[id] = ceremony{data: data, invite: invite, expiry: now.Add(ceremonyTTL)}
	s.mu.Unlock()
	return id, nil
}

// takeCeremony retrieves and removes an in-flight ceremony. A missing or
// expired ceremony yields ok=false.
func (s *Service) takeCeremony(id string) (ceremony, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.ceremonies[id]
	if !ok {
		return ceremony{}, false
	}
	delete(s.ceremonies, id)
	if time.Now().After(c.expiry) {
		return ceremony{}, false
	}
	return c, true
}
