package seeder

import (
	"path/filepath"
	"testing"

	"github.com/jdel/emission/internal/client"
)

func newTestClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSettingsStoreDefaultAndOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), userSettingsFileName)
	s := loadSettingsStore(path, 1<<20) // 1M default

	if got := s.bandwidth("alice"); got != 1<<20 {
		t.Errorf("unset user = %d, want default 1<<20", got)
	}
	if err := s.setBandwidth("alice", 2<<20); err != nil {
		t.Fatal(err)
	}
	if got := s.bandwidth("alice"); got != 2<<20 {
		t.Errorf("alice = %d, want 2<<20", got)
	}
	if got := s.bandwidth("bob"); got != 1<<20 {
		t.Errorf("other user still default? got %d", got)
	}
	if err := s.setBandwidth("alice", 0); err == nil {
		t.Error("zero bandwidth must be rejected")
	}
}

func TestSettingsStoreProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), userSettingsFileName)
	s := loadSettingsStore(path, 1<<20)

	// Default profile is normal.
	if got := s.profileName("alice"); got != profileNormal {
		t.Errorf("default profile = %q, want normal", got)
	}
	if got := s.profileK("alice"); got != 3.33 {
		t.Errorf("default k = %v, want 3.33", got)
	}
	if err := s.setProfile("alice", profileAggressive); err != nil {
		t.Fatal(err)
	}
	if got := s.profileK("alice"); got != 1 {
		t.Errorf("aggressive k = %v, want 1", got)
	}
	if err := s.setProfile("alice", "bogus"); err == nil {
		t.Error("unknown profile must be rejected")
	}
}

func TestProfileKFor(t *testing.T) {
	cases := map[string]float64{
		profileStealth:    10,
		profileNormal:     3.33,
		profileAggressive: 1,
		"":                3.33, // unknown → normal
		"bogus":           3.33,
	}
	for name, want := range cases {
		if got := profileKFor(name); got != want {
			t.Errorf("profileKFor(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSettingsStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), userSettingsFileName)
	s := loadSettingsStore(path, 1<<20)
	if err := s.setBandwidth("alice", 5<<20); err != nil {
		t.Fatal(err)
	}
	if err := s.setProfile("alice", profileStealth); err != nil {
		t.Fatal(err)
	}
	// A fresh store reading the same file must see both overrides; the default
	// differs to prove the values came from disk.
	reloaded := loadSettingsStore(path, 1<<20)
	if got := reloaded.bandwidth("alice"); got != 5<<20 {
		t.Errorf("reloaded bandwidth = %d, want 5<<20", got)
	}
	if got := reloaded.profileName("alice"); got != profileStealth {
		t.Errorf("reloaded profile = %q, want stealth", got)
	}
}

// addFakeSession inserts a session owned by owner with the given per-torrent
// max and a single tracker reporting leechers, bypassing the announce path.
func addFakeSession(m *Manager, id, owner string, max uint64, leechers int64) {
	ts := &trackerState{}
	ts.leechers.Store(leechers)
	s := &session{owner: owner, trackers: []*trackerState{ts}}
	s.maxSpeed.Store(max)
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
}

func TestCappedRateScalesToBudget(t *testing.T) {
	c := newTestClient(t)
	m := New(c, t.TempDir(), 0, false, 1000) // tiny 1000 B/s budget
	t.Cleanup(m.Shutdown)

	// Two of alice's torrents, each with leechers → natural rates that together
	// exceed the budget, so cappedRate must scale them down to fit.
	addFakeSession(m, "a", "alice", 10000, 10)
	addFakeSession(m, "b", "alice", 10000, 10)

	k := profileKFor(profileNormal)
	natural := naturalRate(10000, 1000, 10, k)
	r1 := m.cappedRate("alice", natural)
	r2 := m.cappedRate("alice", natural)
	if r1+r2 > 1000 {
		t.Errorf("sum %d exceeds budget 1000", r1+r2)
	}
	// Equal leechers → roughly equal, near half the budget each.
	if r1 < 400 || r1 > 600 {
		t.Errorf("share = %d, want ~500", r1)
	}
}

func TestCappedRateSingleTorrentClampedToBandwidth(t *testing.T) {
	// One torrent, max far above the user's bandwidth: it must be leecher-
	// weighted against min(max, bandwidth), NOT pinned at the full budget.
	c := newTestClient(t)
	m := New(c, t.TempDir(), 0, false, 1<<20) // 1M budget
	t.Cleanup(m.Shutdown)

	addFakeSession(m, "a", "alice", 10<<20, 4) // max 10M, 4 leechers

	k := profileKFor(profileNormal)
	natural := naturalRate(10<<20, 1<<20, 4, k)
	rate := m.cappedRate("alice", natural)

	// w(4) = 4/(4+3.33) ≈ 0.546 of the 1M ceiling ≈ 572k — well under budget.
	if rate >= 1<<20 {
		t.Errorf("rate %d should be below the 1M budget", rate)
	}
	if rate < 500_000 || rate > 650_000 {
		t.Errorf("rate = %d, want ~572k (0.546 × 1M)", rate)
	}
}

func TestCappedRateUnderBudgetUnchanged(t *testing.T) {
	c := newTestClient(t)
	m := New(c, t.TempDir(), 0, false, 1<<30) // huge budget: never binds
	t.Cleanup(m.Shutdown)

	addFakeSession(m, "a", "alice", 10000, 10)
	natural := naturalRate(10000, 1<<30, 10, profileKFor(profileNormal))
	if got := m.cappedRate("alice", natural); got != natural {
		t.Errorf("under-budget rate = %d, want unchanged %d", got, natural)
	}
}
