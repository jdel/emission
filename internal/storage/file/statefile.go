package file

import (
	"encoding/json"
	"os"

	"github.com/jdel/emission/internal/model"
)

// States persists per-torrent overrides as a <name>.torrent.json sidecar next
// to the .torrent file.
type States struct{}

func statePath(torrentPath string) string { return torrentPath + ".json" }

// Load reads the sidecar if present. ok is false when the file is missing,
// malformed, or values don't pass validation.
func (States) Load(torrentPath string) (model.TorrentState, bool) {
	data, err := os.ReadFile(statePath(torrentPath))
	if err != nil {
		return model.TorrentState{}, false
	}
	var st model.TorrentState
	if err := json.Unmarshal(data, &st); err != nil {
		return model.TorrentState{}, false
	}
	if st.MaxRatio < 0 {
		return model.TorrentState{}, false
	}
	return st, true
}

// Save writes the sidecar atomically, so a crash mid-write never leaves a
// truncated JSON file.
func (States) Save(torrentPath string, st model.TorrentState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(statePath(torrentPath), data, 0o644)
}

// Delete removes the sidecar, ignoring not-exist errors.
func (States) Delete(torrentPath string) {
	_ = os.Remove(statePath(torrentPath))
}
