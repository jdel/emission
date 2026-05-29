package seeder

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// userSettingsFileName is the per-owner settings file, stored in the watched
// root. It is not a .torrent, so the directory watcher ignores it.
const userSettingsFileName = ".emission-users.json"

// Seeding profiles control how steeply a torrent's reported rate ramps with its
// leecher count: k is the leecher count at which it reaches half its max.
// Smaller k ramps to max with fewer leechers (eager); larger k stays low until
// the swarm is large (cautious).
const (
	profileStealth    = "stealth"    // k = 10: only ramps in big swarms
	profileNormal     = "normal"     // k = 3.33: balanced (default)
	profileAggressive = "aggressive" // k = 1: near-max on almost any demand
)

// profileKFor maps a profile name to its half-saturation constant k. Unknown or
// empty names fall back to normal.
func profileKFor(name string) float64 {
	switch name {
	case profileStealth:
		return 10
	case profileAggressive:
		return 1
	default:
		return 3.33 // normal
	}
}

// validProfile reports whether name is a known seeding profile.
func validProfile(name string) bool {
	return name == profileStealth || name == profileNormal || name == profileAggressive
}

// userSettings is one owner's persisted seeding preferences.
type userSettings struct {
	Bandwidth uint64 `json:"bandwidth,omitempty"` // 0 = use the store default
	Profile   string `json:"profile,omitempty"`   // "" = normal
}

// settingsStore holds per-owner seeding preferences (upload-bandwidth ceiling
// and seeding profile), keyed by owner ("" = root / auth-disabled). Explicit
// values persist to a JSON file; unset fields fall back to defaults.
type settingsStore struct {
	mu           sync.Mutex
	path         string
	defBandwidth uint64
	perUser      map[string]userSettings
}

// loadSettingsStore reads persisted settings from path, falling back to an
// empty store (defaults only) when the file is missing or malformed.
func loadSettingsStore(path string, defBandwidth uint64) *settingsStore {
	s := &settingsStore{path: path, defBandwidth: defBandwidth, perUser: map[string]userSettings{}}
	if data, err := os.ReadFile(path); err == nil {
		var m map[string]userSettings
		if json.Unmarshal(data, &m) == nil && m != nil {
			s.perUser = m
		}
	}
	return s
}

// bandwidth returns owner's ceiling, or the default when unset.
func (s *settingsStore) bandwidth(owner string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.perUser[owner]; ok && u.Bandwidth > 0 {
		return u.Bandwidth
	}
	return s.defBandwidth
}

// profileK returns the half-saturation constant for owner's seeding profile.
func (s *settingsStore) profileK(owner string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return profileKFor(s.perUser[owner].Profile)
}

// profileName returns owner's seeding profile, defaulting to normal.
func (s *settingsStore) profileName(owner string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.perUser[owner].Profile; validProfile(p) {
		return p
	}
	return profileNormal
}

// setBandwidth records owner's ceiling and persists. Zero is rejected — a
// finite, positive ceiling is always required (unlimited is not allowed).
func (s *settingsStore) setBandwidth(owner string, bytesPerSec uint64) error {
	if bytesPerSec == 0 {
		return fmt.Errorf("bandwidth must be greater than zero")
	}
	s.mu.Lock()
	u := s.perUser[owner]
	u.Bandwidth = bytesPerSec
	s.perUser[owner] = u
	snapshot := s.snapshotLocked()
	s.mu.Unlock()
	return s.save(snapshot)
}

// setProfile records owner's seeding profile and persists.
func (s *settingsStore) setProfile(owner, name string) error {
	if !validProfile(name) {
		return fmt.Errorf("unknown seeding profile %q", name)
	}
	s.mu.Lock()
	u := s.perUser[owner]
	u.Profile = name
	s.perUser[owner] = u
	snapshot := s.snapshotLocked()
	s.mu.Unlock()
	return s.save(snapshot)
}

func (s *settingsStore) snapshotLocked() map[string]userSettings {
	out := make(map[string]userSettings, len(s.perUser))
	for k, v := range s.perUser {
		out[k] = v
	}
	return out
}

// save atomically writes snapshot to disk via a temp file + rename.
func (s *settingsStore) save(snapshot map[string]userSettings) error {
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
