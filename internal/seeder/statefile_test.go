package seeder

import (
	"path/filepath"
	"testing"
)

func TestStateFileRoundtrip(t *testing.T) {
	// Use a value the old "1.0 KB" human-readable format would have rounded
	// (locking in the lossless byte-count layout).
	path := filepath.Join(t.TempDir(), "x.torrent")
	const ts int64 = 1700000000000
	if err := SaveStateFile(path, 1500000, 2.5, ts, 123456, true); err != nil {
		t.Fatal(err)
	}
	max, ratio, addedAt, uploaded, deleteOnCap, ok := LoadStateFile(path)
	if !ok {
		t.Fatal("LoadStateFile returned ok=false")
	}
	if max != 1500000 {
		t.Errorf("max = %d, want 1500000", max)
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
	if err := SaveStateFile(path, 1024, 0, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	_, ratio, _, _, _, ok := LoadStateFile(path)
	if !ok {
		t.Fatal("LoadStateFile returned ok=false")
	}
	if ratio != 0 {
		t.Errorf("ratio = %v, want 0", ratio)
	}
}

func TestStateFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.torrent")
	if _, _, _, _, _, ok := LoadStateFile(path); ok {
		t.Error("missing state file should return ok=false")
	}
}

func TestStateFileNegativeRatio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	if err := SaveStateFile(path, 1024, -0.5, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, _, ok := LoadStateFile(path); ok {
		t.Error("negative ratio should be rejected on Load")
	}
}
