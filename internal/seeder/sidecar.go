package seeder

import (
	"encoding/json"
	"os"
)

// speedSidecar is the on-disk shape of a per-torrent override, stored next
// to the .torrent as <name>.torrent.json. Speeds are raw bytes/sec so the
// round-trip is lossless; the human-readable display lives in the UI.
// MaxRatio is a multiple of torrent size; 0 means unlimited.
type speedSidecar struct {
	MinSpeed uint64  `json:"minSpeed"`
	MaxSpeed uint64  `json:"maxSpeed"`
	MaxRatio float64 `json:"maxRatio,omitempty"`
}

func sidecarPath(torrentPath string) string { return torrentPath + ".json" }

// LoadSidecar reads a per-torrent override if present. Returns ok=false when
// the file is missing, malformed, or values don't pass validation.
func LoadSidecar(torrentPath string) (minSpeed, maxSpeed uint64, maxRatio float64, ok bool) {
	data, err := os.ReadFile(sidecarPath(torrentPath))
	if err != nil {
		return 0, 0, 0, false
	}
	var sc speedSidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		return 0, 0, 0, false
	}
	if sc.MinSpeed > sc.MaxSpeed || sc.MaxRatio < 0 {
		return 0, 0, 0, false
	}
	return sc.MinSpeed, sc.MaxSpeed, sc.MaxRatio, true
}

// SaveSidecar writes a per-torrent override next to torrentPath atomically:
// the bytes are written to a sibling .tmp file and then renamed into place,
// so a crash mid-write never leaves a truncated JSON file.
func SaveSidecar(torrentPath string, minSpeed, maxSpeed uint64, maxRatio float64) error {
	data, err := json.MarshalIndent(speedSidecar{
		MinSpeed: minSpeed,
		MaxSpeed: maxSpeed,
		MaxRatio: maxRatio,
	}, "", "  ")
	if err != nil {
		return err
	}
	path := sidecarPath(torrentPath)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// removeSidecar deletes the sidecar for torrentPath, ignoring not-exist errors.
func removeSidecar(torrentPath string) {
	_ = os.Remove(sidecarPath(torrentPath))
}
