package auth

import (
	"testing"
	"time"
)

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
