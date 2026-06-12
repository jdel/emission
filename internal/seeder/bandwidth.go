package seeder

import (
	"fmt"
	"sync"

	"github.com/jdel/emission/internal/model"
	"github.com/jdel/emission/internal/storage"
	"github.com/jdel/emission/internal/storage/file"
)

// userSettingsFileName is the per-owner settings file, stored in the watched
// root. It is not a .torrent, so the directory watcher ignores it.
const userSettingsFileName = "client-settings.json"

// Seeding profiles control how steeply a torrent's reported rate ramps with its
// leecher count: halfSaturation is the leecher count at which it reaches half
// its max. A smaller value ramps to max with fewer leechers (eager); a larger
// value stays low until the swarm is large (cautious).
const (
	profileStealth    = "stealth"    // halfSaturation = 10: only ramps in big swarms
	profileNormal     = "normal"     // halfSaturation = 4: balanced (default)
	profileAggressive = "aggressive" // halfSaturation = 1: near-max on almost any demand
)

// Half-saturation bounds for a custom seeding curve (in leechers). The named
// presets all fall within this range.
const (
	minHalfSaturation    = 1
	maxHalfSaturation    = 10
	normalHalfSaturation = 4
)

// halfSaturationForProfile maps a legacy profile name to its half-saturation
// constant, used to migrate older persisted settings. Unknown or empty names
// fall back to normal.
func halfSaturationForProfile(name string) float64 {
	switch name {
	case profileStealth:
		return maxHalfSaturation
	case profileAggressive:
		return minHalfSaturation
	default:
		return normalHalfSaturation
	}
}

// profileNameFor derives a display name from a half-saturation value: the three
// preset constants map to their names, anything else is "custom".
func profileNameFor(k float64) string {
	switch k {
	case minHalfSaturation:
		return profileAggressive
	case maxHalfSaturation:
		return profileStealth
	case 0, normalHalfSaturation:
		return profileNormal
	default:
		return "custom"
	}
}

// settingsStore holds per-owner seeding preferences (upload-bandwidth ceiling
// and seeding profile), keyed by owner ("" = root / auth-disabled). Explicit
// values persist through the settings repository; unset fields fall back to
// defaults.
type settingsStore struct {
	mu           sync.Mutex
	repo         storage.SettingsRepo
	defBandwidth uint64
	perUser      map[string]model.UserSettings
}

// loadSettingsStore reads persisted settings from the file at path, falling
// back to an empty store (defaults only) when none exist or they are
// malformed.
func loadSettingsStore(path string, defBandwidth uint64) *settingsStore {
	s := &settingsStore{repo: file.Settings{Path: path}, defBandwidth: defBandwidth, perUser: map[string]model.UserSettings{}}
	if m, err := s.repo.Load(); err == nil && m != nil {
		// Migrate legacy profile names to a numeric half-saturation.
		for owner, u := range m {
			if u.HalfSaturation == 0 && u.Profile != "" {
				u.HalfSaturation = halfSaturationForProfile(u.Profile)
				u.Profile = ""
				m[owner] = u
			}
		}
		s.perUser = m
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

// profileHalfSaturation returns owner's half-saturation constant, defaulting to
// normal when unset.
func (s *settingsStore) profileHalfSaturation(owner string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k := s.perUser[owner].HalfSaturation; k > 0 {
		return k
	}
	return normalHalfSaturation
}

// profileName returns the display name for owner's seeding curve.
func (s *settingsStore) profileName(owner string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return profileNameFor(s.perUser[owner].HalfSaturation)
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
	return s.repo.Save(snapshot)
}

// setHalfSaturation records owner's seeding-curve half-saturation (leechers for
// half speed) and persists. The value must lie within the allowed bounds.
func (s *settingsStore) setHalfSaturation(owner string, k float64) error {
	if k < minHalfSaturation || k > maxHalfSaturation {
		return fmt.Errorf("half-saturation must be between %d and %d leechers", minHalfSaturation, maxHalfSaturation)
	}
	s.mu.Lock()
	u := s.perUser[owner]
	u.HalfSaturation = k
	u.Profile = ""
	s.perUser[owner] = u
	snapshot := s.snapshotLocked()
	s.mu.Unlock()
	return s.repo.Save(snapshot)
}

// proxy returns owner's explicit proxy URL and whether one is set. When unset,
// the caller falls back to the server default.
func (s *settingsStore) proxy(owner string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.perUser[owner].Proxy; p != nil {
		return *p, true
	}
	return "", false
}

// setProxy records owner's explicit proxy URL ("" = announce directly) and
// persists it.
func (s *settingsStore) setProxy(owner, proxyURL string) error {
	s.mu.Lock()
	u := s.perUser[owner]
	u.Proxy = &proxyURL
	s.perUser[owner] = u
	snapshot := s.snapshotLocked()
	s.mu.Unlock()
	return s.repo.Save(snapshot)
}

func (s *settingsStore) snapshotLocked() map[string]model.UserSettings {
	out := make(map[string]model.UserSettings, len(s.perUser))
	for k, v := range s.perUser {
		out[k] = v
	}
	return out
}
