package seeder

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// torrentState is the on-disk shape of a per-torrent override, stored next
// to the .torrent as <name>.torrent.json. Speeds are raw bytes/sec so the
// round-trip is lossless; the human-readable display lives in the UI.
// MaxRatio is a multiple of torrent size; 0 means unlimited.
// AddedAt is unix milliseconds; 0 means unknown (old state file without it).
type torrentState struct {
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
func LoadStateFile(torrentPath string) (maxSpeed uint64, maxRatio float64, addedAt int64, uploadedBytes uint64, deleteOnCap bool, ok bool) {
	data, err := os.ReadFile(stateFilePath(torrentPath))
	if err != nil {
		return 0, 0, 0, 0, false, false
	}
	var sc torrentState
	if err := json.Unmarshal(data, &sc); err != nil {
		return 0, 0, 0, 0, false, false
	}
	if sc.MaxRatio < 0 {
		return 0, 0, 0, 0, false, false
	}
	return sc.MaxSpeed, sc.MaxRatio, sc.AddedAt, sc.UploadedBytes, sc.DeleteOnCap, true
}

// SaveStateFile writes a per-torrent override next to torrentPath atomically:
// the bytes are written to a sibling .tmp file and then renamed into place,
// so a crash mid-write never leaves a truncated JSON file.
// addedAt is unix milliseconds; 0 is stored as absent (omitempty).
func SaveStateFile(torrentPath string, maxSpeed uint64, maxRatio float64, addedAt int64, uploadedBytes uint64, deleteOnCap bool) error {
	data, err := json.MarshalIndent(torrentState{
		MaxSpeed:      maxSpeed,
		MaxRatio:      maxRatio,
		AddedAt:       addedAt,
		UploadedBytes: uploadedBytes,
		DeleteOnCap:   deleteOnCap,
	}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(stateFilePath(torrentPath), data, 0o644)
}

// atomicWrite writes data to path atomically: a uniquely-named temp file in the
// same directory, then rename into place. The unique name lets concurrent
// writers to the same path proceed without clobbering each other's temp file.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil { // CreateTemp makes 0600
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// removeStateFile deletes the state file for torrentPath, ignoring not-exist errors.
func removeStateFile(torrentPath string) {
	_ = os.Remove(stateFilePath(torrentPath))
}
