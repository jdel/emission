// Package storage defines the repository interfaces the application logic
// persists through. Implementations live in subpackages (storage/file);
// callers depend only on these interfaces so the backend can be swapped.
package storage

import "github.com/jdel/emission/internal/model"

// TorrentStateRepo persists per-torrent overrides, keyed by the absolute path
// of the backing .torrent file.
type TorrentStateRepo interface {
	// Load returns the stored state. ok is false when none exists or the
	// stored value is invalid.
	Load(torrentPath string) (st model.TorrentState, ok bool)
	// Save persists the state, overwriting any previous value.
	Save(torrentPath string, st model.TorrentState) error
	// Delete removes the stored state; missing state is a no-op.
	Delete(torrentPath string)
}

// StatsRepo persists a torrent's rate-history samples, keyed by the absolute
// path of the backing .torrent file.
type StatsRepo interface {
	// Load returns the stored history in append order.
	Load(torrentPath string) ([]model.StatsPoint, error)
	// Append adds points to the existing history (cheap, O(new)).
	Append(torrentPath string, pts []model.StatsPoint) error
	// Rewrite atomically replaces the whole history (compaction).
	Rewrite(torrentPath string, pts []model.StatsPoint) error
	// Delete removes the stored history; missing history is a no-op.
	Delete(torrentPath string)
}

// SettingsRepo persists the per-owner settings map.
type SettingsRepo interface {
	// Load returns the persisted settings map.
	Load() (map[string]model.UserSettings, error)
	// Save persists the settings map, overwriting any previous value.
	Save(map[string]model.UserSettings) error
}

// CredentialRepo persists the WebAuthn credential set.
type CredentialRepo interface {
	// Load returns the stored set. ok is false when no set has been saved yet
	// (fresh install); err reports an unreadable or malformed store.
	Load() (cs model.CredentialSet, ok bool, err error)
	// Save persists the credential set, overwriting any previous value.
	Save(cs model.CredentialSet) error
}
