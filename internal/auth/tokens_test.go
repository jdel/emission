package auth

import (
	"testing"
	"time"
)

func TestSessionStoreEvictsExpired(t *testing.T) {
	s := NewSessionStore(20 * time.Millisecond)
	if _, err := s.Create("alice"); err != nil { // abandoned: never looked up again
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond) // let it expire
	if _, err := s.Create("bob"); err != nil { // a later Create must sweep the expired one
		t.Fatal(err)
	}
	if len(s.sessions) != 1 {
		t.Fatalf("expired session not evicted: %d entries, want 1", len(s.sessions))
	}
}

func TestInviteStoreEvictsExpired(t *testing.T) {
	s := NewInviteStore(20 * time.Millisecond)
	if _, err := s.Create("alice", "admin"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := s.Create("bob", "admin"); err != nil {
		t.Fatal(err)
	}
	if len(s.tokens) != 1 {
		t.Fatalf("expired invite not evicted: %d entries, want 1", len(s.tokens))
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := NewSessionStore(time.Hour)
	id, err := s.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	name, ok := s.Lookup(id)
	if !ok || name != "alice" {
		t.Errorf("Lookup = (%q, %v), want (alice, true)", name, ok)
	}
	if _, ok := s.Lookup("bogus"); ok {
		t.Error("unknown session should be invalid")
	}
	s.Delete(id)
	if _, ok := s.Lookup(id); ok {
		t.Error("deleted session should be invalid")
	}
}

func TestSessionExpiry(t *testing.T) {
	s := NewSessionStore(-time.Second) // already expired on create
	id, _ := s.Create("alice")
	if _, ok := s.Lookup(id); ok {
		t.Error("expired session should be invalid")
	}
}

func TestInviteSingleUse(t *testing.T) {
	s := NewInviteStore(time.Hour)
	tok, err := s.Create("alice", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if name, ok := s.Lookup(tok); !ok || name != "alice" {
		t.Errorf("Lookup = (%q, %v), want (alice, true)", name, ok)
	}
	name, createdBy, ok := s.Consume(tok)
	if !ok || name != "alice" || createdBy != "admin" {
		t.Errorf("first consume = (%q, %q, %v), want (alice, admin, true)", name, createdBy, ok)
	}
	if _, _, ok := s.Consume(tok); ok {
		t.Error("second consume should fail (single-use)")
	}
}

func TestInviteExpiry(t *testing.T) {
	s := NewInviteStore(-time.Second)
	tok, _ := s.Create("alice", "admin")
	if _, _, ok := s.Consume(tok); ok {
		t.Error("expired invite should not consume")
	}
}
