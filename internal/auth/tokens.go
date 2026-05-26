package auth

import (
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

// randomToken returns a 64-character hex string from 32 random bytes —
// unguessable, safe as a session id or invite token.
func randomToken() (string, error) {
	b, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// sessionEntry is one live login session: its username and sliding expiry.
type sessionEntry struct {
	username string
	expiry   time.Time
}

// SessionStore holds active login sessions in memory. Sessions slide: every
// successful Lookup extends the expiry by the configured TTL. Sessions do not
// survive a restart — the user simply logs in again.
type SessionStore struct {
	ttl      time.Duration
	mu       sync.Mutex
	sessions map[string]sessionEntry
}

// NewSessionStore creates a session store with the given sliding TTL.
func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{ttl: ttl, sessions: make(map[string]sessionEntry)}
}

// Create starts a new session for username and returns its id.
func (s *SessionStore) Create(username string) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.sessions[id] = sessionEntry{username: username, expiry: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return id, nil
}

// Lookup returns the username for a live session and slides its expiry. An
// unknown or expired session yields ok=false.
func (s *SessionStore) Lookup(id string) (username string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[id]
	if !ok {
		return "", false
	}
	if time.Now().After(e.expiry) {
		delete(s.sessions, id)
		return "", false
	}
	e.expiry = time.Now().Add(s.ttl)
	s.sessions[id] = e
	return e.username, true
}

// Delete ends a session (logout).
func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// inviteEntry is one pending device-registration token: the username it
// enrols and its expiry. An empty username marks a bootstrap invite, where the
// first user names themselves.
type inviteEntry struct {
	username  string
	createdBy string
	expiry    time.Time
}

// InviteStore holds one-time device-registration tokens in memory. A token is
// valid until consumed once or until its fixed TTL elapses.
type InviteStore struct {
	ttl    time.Duration
	mu     sync.Mutex
	tokens map[string]inviteEntry
}

// NewInviteStore creates an invite store with the given token TTL.
func NewInviteStore(ttl time.Duration) *InviteStore {
	return &InviteStore{ttl: ttl, tokens: make(map[string]inviteEntry)}
}

// Create mints a new invite token enrolling the given username. createdBy is
// the username of whoever created the invite (stored for audit). The token is
// a 3-word code (e.g. "arrogant-jimmy-dumpster") — easy to read aloud.
func (s *InviteStore) Create(username, createdBy string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for attempt := 0; attempt < 8; attempt++ {
		tok, err := randomWords(3)
		if err != nil {
			return "", err
		}
		if _, taken := s.tokens[tok]; taken {
			continue // astronomically unlikely; retry anyway
		}
		s.tokens[tok] = inviteEntry{username: username, createdBy: createdBy, expiry: time.Now().Add(s.ttl)}
		return tok, nil
	}
	return "", fmt.Errorf("could not allocate a unique invite token")
}

// Lookup returns a live invite's username without consuming it — used to fail
// fast and to show the registrant who the invite is for.
func (s *InviteStore) Lookup(token string) (username string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tokens[token]
	if !ok || time.Now().After(e.expiry) {
		return "", false
	}
	return e.username, true
}

// PendingInvite is a summary of one unclaimed invite token.
type PendingInvite struct {
	Token     string
	Username  string
	CreatedBy string
	ExpiresAt time.Time
}

// List returns all non-expired pending invites, ordered by expiry (soonest first).
func (s *InviteStore) List() []PendingInvite {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	out := make([]PendingInvite, 0, len(s.tokens))
	for tok, e := range s.tokens {
		if !now.After(e.expiry) {
			out = append(out, PendingInvite{Token: tok, Username: e.username, CreatedBy: e.createdBy, ExpiresAt: e.expiry})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	return out
}

// Revoke deletes a pending invite. Returns false if the token was not found.
func (s *InviteStore) Revoke(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tokens[token]; !ok {
		return false
	}
	delete(s.tokens, token)
	return true
}

// Consume validates a token, removes it, and returns its username and creator.
// A token can be used at most once. ok is false for an unknown or expired token.
func (s *InviteStore) Consume(token string) (username, createdBy string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tokens[token]
	if !ok {
		return "", "", false
	}
	delete(s.tokens, token) // single-use: gone regardless of the outcome
	if time.Now().After(e.expiry) {
		return "", "", false
	}
	return e.username, e.createdBy, true
}
