package seeder

import (
	"path/filepath"
	"testing"
)

func TestStateFileRoundtrip(t *testing.T) {
	// Use a value the old "1.0 KB" human-readable format would have rounded
	// (2000 → "2.0 KB" → 2048), locking in the lossless byte-count layout.
	path := filepath.Join(t.TempDir(), "x.torrent")
	const ts int64 = 1700000000000
	if err := SaveStateFile(path, 2000, 1500000, 2.5, ts, 123456, true); err != nil {
		t.Fatal(err)
	}
	min, max, ratio, addedAt, uploaded, deleteOnCap, ok := LoadStateFile(path)
	if !ok {
		t.Fatal("LoadStateFile returned ok=false")
	}
	if min != 2000 || max != 1500000 {
		t.Errorf("speeds = %d/%d, want 2000/1500000", min, max)
	}
	if ratio != 2.5 {
		t.Errorf("ratio = %v, want 2.5", ratio)
	}
	if addedAt != ts {
		t.Errorf("addedAt = %d, want %d", addedAt, ts)
	}
	if uploaded != 123456 {
		t.Errorf("uploadedBytes = %d, want 123456", uploaded)
	}
	if !deleteOnCap {
		t.Error("deleteOnCap = false, want true")
	}
}

func TestStateFileZeroRatioRoundtrip(t *testing.T) {
	// 0 ratio is the "unlimited" sentinel. Must survive Save+Load.
	path := filepath.Join(t.TempDir(), "x.torrent")
	if err := SaveStateFile(path, 0, 1024, 0, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	_, _, ratio, _, _, _, ok := LoadStateFile(path)
	if !ok {
		t.Fatal("LoadStateFile returned ok=false")
	}
	if ratio != 0 {
		t.Errorf("ratio = %v, want 0", ratio)
	}
}

func TestStateFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.torrent")
	if _, _, _, _, _, _, ok := LoadStateFile(path); ok {
		t.Error("missing state file should return ok=false")
	}
}

func TestStateFileMinExceedsMax(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	// SaveStateFile does not validate, so write directly via Save.
	if err := SaveStateFile(path, 1024, 100, 0, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, _, ok := LoadStateFile(path); ok {
		t.Error("min > max should be rejected on Load")
	}
}

func TestStateFileNegativeRatio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	if err := SaveStateFile(path, 100, 1024, -0.5, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, _, ok := LoadStateFile(path); ok {
		t.Error("negative ratio should be rejected on Load")
	}
}
