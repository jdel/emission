package seeder

import (
	"github.com/jdel/emission/internal/model"
	"github.com/jdel/emission/internal/storage/file"
)

// LoadStateFile and SaveStateFile are thin wrappers over the file-backed
// state repository, kept for callers outside the Manager (the upload handler
// pre-seeds overrides next to a freshly written .torrent).

// LoadStateFile reads a per-torrent override if present. Returns ok=false when
// none exists or values don't pass validation. addedAt is unix milliseconds;
// 0 means the state predates this field.
func LoadStateFile(torrentPath string) (maxSpeed uint64, maxRatio float64, addedAt int64, uploadedBytes uint64, deleteOnCap bool, ok bool) {
	st, ok := file.States{}.Load(torrentPath)
	return st.MaxSpeed, st.MaxRatio, st.AddedAt, st.UploadedBytes, st.DeleteOnCap, ok
}

// SaveStateFile persists a per-torrent override next to torrentPath.
// addedAt is unix milliseconds; 0 is stored as absent.
func SaveStateFile(torrentPath string, maxSpeed uint64, maxRatio float64, addedAt int64, uploadedBytes uint64, deleteOnCap bool) error {
	return file.States{}.Save(torrentPath, model.TorrentState{
		MaxSpeed:      maxSpeed,
		MaxRatio:      maxRatio,
		AddedAt:       addedAt,
		UploadedBytes: uploadedBytes,
		DeleteOnCap:   deleteOnCap,
	})
}
