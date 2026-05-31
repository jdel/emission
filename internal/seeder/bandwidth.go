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

// userSettings is one owner's persisted seeding preferences.
type userSettings struct {
	Bandwidth      uint64  `json:"bandwidth,omitempty"`      // 0 = use the store default
	HalfSaturation float64 `json:"halfSaturation,omitempty"` // 0 = normal; leechers for half speed
	Profile        string  `json:"profile,omitempty"`        // legacy; migrated to HalfSaturation on load
	Proxy          *string `json:"proxy,omitempty"`          // nil = use server default; set (incl "") = explicit ("" = direct)
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
	return s.save(snapshot)
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
	return s.save(snapshot)
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
