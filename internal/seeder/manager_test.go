package seeder

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdel/emission/internal/client"
	"github.com/jdel/emission/internal/tracker"
)

// newTrackerServer returns an httptest server that answers every announce
// with a long interval so sessions stop chattering after their first call.
// The server is closed via t.Cleanup after the Manager (cleanup runs LIFO,
// so Manager.Shutdown runs first and its stopped-announces hit a live
// server instead of "connection refused").
func newTrackerServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("d8:completei0e10:incompletei0e8:intervali999999ee"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newTestTorrent writes a minimal valid single-file .torrent into dir with the
// given display name and announce URL, and returns the absolute path.
func newTestTorrent(t *testing.T, dir, name, announceURL string) string {
	t.Helper()
	const pieces = "01234567890123456789" // exactly 20 bytes
	body := fmt.Sprintf("d8:announce%d:%s4:infod6:lengthi1024e4:name%d:%s12:piece lengthi16384e6:pieces20:%see",
		len(announceURL), announceURL, len(name), name, pieces)
	path := filepath.Join(dir, name+".torrent")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestManager(t *testing.T, dir string) *Manager {
	t.Helper()
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	m := New(c, dir, 0, false, 1<<30, "") // generous bandwidth: never the binding constraint here
	t.Cleanup(m.Shutdown)
	return m
}

func TestManagerAddFileHappy(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)
	path := newTestTorrent(t, dir, "alpha", srv.URL)

	st, err := m.AddFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "alpha" {
		t.Errorf("name = %q, want alpha", st.Name)
	}
	if st.SizeBytes != 1024 {
		t.Errorf("size = %d, want 1024", st.SizeBytes)
	}
	if st.MaxRateBytesPerSec != m.DefaultBandwidth() {
		t.Errorf("max speed = %d, want default bandwidth %d", st.MaxRateBytesPerSec, m.DefaultBandwidth())
	}
	if !m.Exists(st.ID) {
		t.Error("torrent missing after AddFile")
	}
}

func TestManagerAddFilePrivateFlag(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)

	// Public torrent (no private key) → Status.Private false.
	pub, err := m.AddFile(newTestTorrent(t, dir, "pub", srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if pub.Private {
		t.Error("public torrent reported Private=true")
	}

	// Private torrent: info dict carries private=1 → Status.Private true.
	const pieces = "01234567890123456789"
	name, announce := "priv", srv.URL
	body := fmt.Sprintf("d8:announce%d:%s4:infod6:lengthi1024e4:name%d:%s12:piece lengthi16384e6:pieces20:%s7:privatei1eee",
		len(announce), announce, len(name), name, pieces)
	path := filepath.Join(dir, "priv.torrent")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	priv, err := m.AddFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !priv.Private {
		t.Error("private torrent reported Private=false")
	}
}

func TestManagerAddFileDuplicate(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)
	path := newTestTorrent(t, dir, "x", srv.URL)
	if _, err := m.AddFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddFile(path); err == nil {
		t.Error("duplicate AddFile should error")
	}
}

func TestManagerAddFileUsesStateFile(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)
	path := newTestTorrent(t, dir, "x", srv.URL)

	// Pre-write a state file with values different from the AddFile args.
	if err := SaveStateFile(path, 1024, 1.5, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	st, err := m.AddFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.MaxRateBytesPerSec != 1024 || st.MaxRatio != 1.5 {
		t.Errorf("state file not applied: %+v", st)
	}
}

func TestManagerSetClientOptions(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)
	path := newTestTorrent(t, dir, "x", srv.URL)
	st, _ := m.AddFile(path)

	if err := m.SetClientOptions(st.ID, 1024, 2.0, false); err != nil {
		t.Fatal(err)
	}
	after, ok := m.Get(st.ID)
	if !ok {
		t.Fatal("Get returned ok=false")
	}
	if after.MaxRateBytesPerSec != 1024 {
		t.Errorf("max = %d, want 1024", after.MaxRateBytesPerSec)
	}
	if after.MaxRatio != 2.0 {
		t.Errorf("ratio = %v, want 2.0", after.MaxRatio)
	}

	// SetClientOptions persists to state file.
	max, ratio, _, _, _, ok := LoadStateFile(path)
	if !ok || max != 1024 || ratio != 2.0 {
		t.Errorf("state file = (%d, %v, %v)", max, ratio, ok)
	}
}

func TestManagerSetClientOptionsValidation(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)
	path := newTestTorrent(t, dir, "x", srv.URL)
	st, _ := m.AddFile(path)

	if err := m.SetClientOptions(st.ID, 500, -1, false); err == nil {
		t.Error("negative ratio should error")
	}
	if err := m.SetClientOptions("deadbeef", 500, 0, false); err == nil {
		t.Error("unknown id should error")
	}
}

func TestManagerRemove(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)
	path := newTestTorrent(t, dir, "x", srv.URL)
	st, _ := m.AddFile(path)

	if err := m.Remove(st.ID); err != nil {
		t.Fatal(err)
	}
	if m.Exists(st.ID) {
		t.Error("torrent still exists after Remove")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error(".torrent file should be deleted")
	}
}

func TestManagerRemoveUnknown(t *testing.T) {
	dir := t.TempDir()
	m := newTestManager(t, dir)
	if err := m.Remove("deadbeef"); err == nil {
		t.Error("Remove of unknown id should error")
	}
}

func TestManagerRemoveByPath(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)
	path := newTestTorrent(t, dir, "x", srv.URL)
	st, _ := m.AddFile(path)

	m.RemoveByPath(path)
	if m.Exists(st.ID) {
		t.Error("session not stopped after RemoveByPath")
	}
	// File should still be on disk (RemoveByPath does not delete).
	if _, err := os.Stat(path); err != nil {
		t.Errorf(".torrent should still exist: %v", err)
	}
}

func TestManagerRemoveUnder(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)

	sub := filepath.Join(dir, "alice")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	subPath := newTestTorrent(t, sub, "private", srv.URL)
	rootPath := newTestTorrent(t, dir, "shared", srv.URL)
	sub1, _ := m.AddFile(subPath)
	root1, _ := m.AddFile(rootPath)

	m.RemoveUnder(sub)
	if m.Exists(sub1.ID) {
		t.Error("session under sub dir should be stopped")
	}
	if !m.Exists(root1.ID) {
		t.Error("root-level session should survive RemoveUnder")
	}
}

func TestManagerPageAndFilter(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)
	for _, n := range []string{"alpha", "beta", "gamma"} {
		path := newTestTorrent(t, dir, n, srv.URL)
		if _, err := m.AddFile(path); err != nil {
			t.Fatal(err)
		}
	}

	items, total := m.Page(0, 10, "", "", "")
	if total != 3 || len(items) != 3 {
		t.Errorf("no filter: items=%d total=%d, want 3/3", len(items), total)
	}
	items, total = m.Page(0, 10, "alp", "", "")
	if total != 1 || len(items) != 1 || items[0].Name != "alpha" {
		t.Errorf("filter alp: %+v total=%d", items, total)
	}
	items, total = m.Page(1, 1, "", "", "")
	if total != 3 || len(items) != 1 {
		t.Errorf("page slice: items=%d total=%d, want 1/3", len(items), total)
	}
	items, _ = m.Page(99, 10, "", "", "")
	if len(items) != 0 {
		t.Errorf("offset past end: got %d items", len(items))
	}

	// A torrent under a user subdirectory is filterable by owner.
	ownedDir := filepath.Join(dir, "alice")
	if err := os.MkdirAll(ownedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddFile(newTestTorrent(t, ownedDir, "delta", srv.URL)); err != nil {
		t.Fatal(err)
	}
	items, total = m.Page(0, 10, "", "", "alice")
	if total != 1 || len(items) != 1 || items[0].Name != "delta" {
		t.Errorf("owner alice: %+v total=%d, want 1×delta", items, total)
	}
	if _, total = m.Page(0, 10, "", "", ""); total != 4 {
		t.Errorf("no filter after add: total=%d, want 4", total)
	}
}

func TestManagerVisible(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)

	rootPath := newTestTorrent(t, dir, "shared", srv.URL)
	if _, err := m.AddFile(rootPath); err != nil {
		t.Fatal(err)
	}
	aliceDir := filepath.Join(dir, "alice")
	if err := os.MkdirAll(aliceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alicePath := newTestTorrent(t, aliceDir, "secret", srv.URL)
	if _, err := m.AddFile(alicePath); err != nil {
		t.Fatal(err)
	}

	// Admin (viewer == "") sees everything.
	if got := m.Visible(""); len(got) != 2 {
		t.Errorf("admin sees %d, want 2", len(got))
	}
	// alice sees her own + the shared one.
	alice := m.Visible("alice")
	if len(alice) != 2 {
		t.Errorf("alice sees %d, want 2", len(alice))
	}
	// bob sees only shared.
	bob := m.Visible("bob")
	if len(bob) != 1 || bob[0].Name != "shared" {
		t.Errorf("bob sees %+v, want only shared", bob)
	}
}

func TestManagerSubscribeFiresOnAdd(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)
	ch, cancel := m.Subscribe()
	defer cancel()

	path := newTestTorrent(t, dir, "x", srv.URL)
	if _, err := m.AddFile(path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no notification after AddFile")
	}
}

func TestManagerSubscribeCoalesces(t *testing.T) {
	srv := newTrackerServer(t)
	dir := t.TempDir()
	m := newTestManager(t, dir)
	ch, cancel := m.Subscribe()
	defer cancel()

	// Two adds back-to-back without draining the channel — second send
	// should be silently dropped (channel depth 1).
	p1 := newTestTorrent(t, dir, "a", srv.URL)
	p2 := newTestTorrent(t, dir, "b", srv.URL)
	if _, err := m.AddFile(p1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddFile(p2); err != nil {
		t.Fatal(err)
	}

	// First receive succeeds.
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("first notification missing")
	}
	// Second receive should NOT fire instantly (no queued event).
	select {
	case <-ch:
		t.Error("expected coalesced notification — got a second signal")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestManagerSubscribeUnsubscribeCloses(t *testing.T) {
	dir := t.TempDir()
	m := newTestManager(t, dir)
	ch, cancel := m.Subscribe()
	cancel()
	if _, ok := <-ch; ok {
		t.Error("channel should be closed after unsubscribe")
	}
}

func TestOwner(t *testing.T) {
	cases := map[string]string{
		"alice" + string(filepath.Separator) + "movie.torrent":                                   "alice",
		"bob" + string(filepath.Separator) + "sub" + string(filepath.Separator) + "clip.torrent": "bob",
		"loose.torrent": "",
		"":              "",
	}
	for location, want := range cases {
		if got := Owner(location); got != want {
			t.Errorf("Owner(%q) = %q, want %q", location, got, want)
		}
	}
}

func TestRelPath(t *testing.T) {
	dir := t.TempDir()
	m := newTestManager(t, dir)
	abs := filepath.Join(dir, "alice", "x.torrent")
	if got := m.relPath(abs); !strings.HasSuffix(got, "x.torrent") {
		t.Errorf("relPath = %q", got)
	}
	// Outside-root path falls back to base name.
	outside := filepath.Join(t.TempDir(), "elsewhere.torrent")
	if got := m.relPath(outside); got != "elsewhere.torrent" {
		t.Errorf("outside-root relPath = %q, want elsewhere.torrent", got)
	}
}

func TestTrackerStateStoresTrackerID(t *testing.T) {
	ts := &trackerState{url: "http://t.example/a"}

	// First response issues an ID.
	ts.apply(&tracker.Response{TrackerID: "tid-1"}, nil)
	if got, _ := ts.trackerID.Load().(string); got != "tid-1" {
		t.Errorf("trackerID = %q, want tid-1", got)
	}

	// A later response without an ID must not clobber the stored one.
	ts.apply(&tracker.Response{}, nil)
	if got, _ := ts.trackerID.Load().(string); got != "tid-1" {
		t.Errorf("trackerID overwritten to %q, want tid-1", got)
	}
}

func TestClientForPerOwner(t *testing.T) {
	m := newTestManager(t, t.TempDir())

	alice1, err := m.clientFor("alice")
	if err != nil {
		t.Fatal(err)
	}
	alice2, err := m.clientFor("alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := m.clientFor("bob")
	if err != nil {
		t.Fatal(err)
	}

	if alice1 != alice2 {
		t.Error("same owner should reuse one client instance")
	}
	if alice1.PeerID == bob.PeerID {
		t.Errorf("different owners share a peer ID: %q", alice1.PeerID)
	}
	if alice1.PeerID == m.client.PeerID {
		t.Error("clone reused the template peer ID")
	}
}
