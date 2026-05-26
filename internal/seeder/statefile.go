package seeder

import (
	"encoding/json"
	"os"
)

// torrentState is the on-disk shape of a per-torrent override, stored next
// to the .torrent as <name>.torrent.json. Speeds are raw bytes/sec so the
// round-trip is lossless; the human-readable display lives in the UI.
// MaxRatio is a multiple of torrent size; 0 means unlimited.
// AddedAt is unix milliseconds; 0 means unknown (old state file without it).
type torrentState struct {
	MinSpeed      uint64  `json:"minSpeed"`
	MaxSpeed      uint64  `json:"maxSpeed"`
	MaxRatio      float64 `json:"maxRatio,omitempty"`
	AddedAt       int64   `json:"addedAt,omitempty"`
	UploadedBytes uint64  `json:"uploadedBytes,omitempty"`
	DeleteOnCap   bool    `json:"deleteOnCap,omitempty"`
}

func stateFilePath(torrentPath string) string { return torrentPath + ".json" }

// LoadStateFile reads a per-torrent override if present. Returns ok=false when
// the file is missing, malformed, or values don't pass validation.
// addedAt is unix milliseconds; 0 means the state file predates this field.
func LoadStateFile(torrentPath string) (minSpeed, maxSpeed uint64, maxRatio float64, addedAt int64, uploadedBytes uint64, deleteOnCap bool, ok bool) {
	data, err := os.ReadFile(stateFilePath(torrentPath))
	if err != nil {
		return 0, 0, 0, 0, 0, false, false
	}
	var sc torrentState
	if err := json.Unmarshal(data, &sc); err != nil {
		return 0, 0, 0, 0, 0, false, false
	}
	if sc.MinSpeed > sc.MaxSpeed || sc.MaxRatio < 0 {
		return 0, 0, 0, 0, 0, false, false
	}
	return sc.MinSpeed, sc.MaxSpeed, sc.MaxRatio, sc.AddedAt, sc.UploadedBytes, sc.DeleteOnCap, true
}

// SaveStateFile writes a per-torrent override next to torrentPath atomically:
// the bytes are written to a sibling .tmp file and then renamed into place,
// so a crash mid-write never leaves a truncated JSON file.
// addedAt is unix milliseconds; 0 is stored as absent (omitempty).
func SaveStateFile(torrentPath string, minSpeed, maxSpeed uint64, maxRatio float64, addedAt int64, uploadedBytes uint64, deleteOnCap bool) error {
	data, err := json.MarshalIndent(torrentState{
		MinSpeed:      minSpeed,
		MaxSpeed:      maxSpeed,
		MaxRatio:      maxRatio,
		AddedAt:       addedAt,
		UploadedBytes: uploadedBytes,
		DeleteOnCap:   deleteOnCap,
	}, "", "  ")
	if err != nil {
		return err
	}
	path := stateFilePath(torrentPath)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// removeStateFile deletes the state file for torrentPath, ignoring not-exist errors.
func removeStateFile(torrentPath string) {
	_ = os.Remove(stateFilePath(torrentPath))
}
