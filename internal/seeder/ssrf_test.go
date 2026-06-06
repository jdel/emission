package seeder

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestAddFileAnnounceBlocksLoopbackTracker reproduces the SSRF regression
// (continuous-improvement finding #1). A torrent whose tracker resolves to a
// loopback/internal address must never be announced to.
//
// The existing guard test (TestDefaultClientBlocksPrivateAddresses) calls
// Announce with Params{} — i.e. HTTPClient==nil — so it only exercises the
// guarded package-level defaultClient. The seeder always injects its own
// client (Manager.httpClient) and so takes a different branch; this test drives
// the real path through AddFile, which the regression in 5d5a4ff left unguarded.
//
// FAILS on current code (the manager's direct client has no dial guard), PASSES
// once Manager.httpClient uses tracker.GuardedDialContext.
func TestAddFileAnnounceBlocksLoopbackTracker(t *testing.T) {
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case hit <- struct{}{}:
		default:
		}
		_, _ = w.Write([]byte("d8:completei7e10:incompletei0e8:intervali3600ee"))
	}))
	t.Cleanup(srv.Close) // httptest listens on 127.0.0.1 — a loopback tracker

	dir := t.TempDir()
	m := newTestManager(t, dir)
	if _, err := m.AddFile(newTestTorrent(t, dir, "ssrf", srv.URL)); err != nil {
		t.Fatal(err)
	}

	// The session fires its EventStarted announce as soon as it starts. If that
	// request reaches our loopback server, the SSRF guard was bypassed.
	select {
	case <-hit:
		t.Fatalf("announce reached loopback tracker %s — SSRF guard bypassed (regression of 2332dc2)", srv.URL)
	case <-time.After(2 * time.Second):
		// Not contacted: the guarded dialer refused the loopback address, as intended.
	}
}
