package seeder

import (
	"path/filepath"
	"testing"
)

func TestSidecarRoundtrip(t *testing.T) {
	// Use a value the old "1.0 KB" human-readable format would have rounded
	// (2000 → "2.0 KB" → 2048), locking in the lossless byte-count layout.
	path := filepath.Join(t.TempDir(), "x.torrent")
	if err := SaveSidecar(path, 2000, 1500000, 2.5); err != nil {
		t.Fatal(err)
	}
	min, max, ratio, ok := LoadSidecar(path)
	if !ok {
		t.Fatal("LoadSidecar returned ok=false")
	}
	if min != 2000 || max != 1500000 {
		t.Errorf("speeds = %d/%d, want 2000/1500000", min, max)
	}
	if ratio != 2.5 {
		t.Errorf("ratio = %v, want 2.5", ratio)
	}
}

func TestSidecarZeroRatioRoundtrip(t *testing.T) {
	// 0 ratio is the "unlimited" sentinel. Must survive Save+Load.
	path := filepath.Join(t.TempDir(), "x.torrent")
	if err := SaveSidecar(path, 0, 1024, 0); err != nil {
		t.Fatal(err)
	}
	_, _, ratio, ok := LoadSidecar(path)
	if !ok {
		t.Fatal("LoadSidecar returned ok=false")
	}
	if ratio != 0 {
		t.Errorf("ratio = %v, want 0", ratio)
	}
}

func TestSidecarMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.torrent")
	if _, _, _, ok := LoadSidecar(path); ok {
		t.Error("missing sidecar should return ok=false")
	}
}

func TestSidecarMinExceedsMax(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	// SaveSidecar does not validate, so write directly via Save.
	if err := SaveSidecar(path, 1024, 100, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := LoadSidecar(path); ok {
		t.Error("min > max should be rejected on Load")
	}
}

func TestSidecarNegativeRatio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	if err := SaveSidecar(path, 100, 1024, -0.5); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := LoadSidecar(path); ok {
		t.Error("negative ratio should be rejected on Load")
	}
}
