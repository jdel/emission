package seeder

import (
	"path/filepath"
	"sync"
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

// TestStateFileConcurrentSave hammers one path from many goroutines, as the
// per-tracker announce loops do for a multi-tracker torrent. With a shared temp
// name, one goroutine's rename removes the temp out from under another (rename
// ENOENT), and concurrent O_TRUNC writes interleave into a torn file. The
// unique-temp-name atomic write must leave every save error-free and the final
// file a complete, parseable state — never a torn one.
func TestStateFileConcurrentSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	const goroutines = 32
	const iterations = 50

	// Distinct, different-length JSON per writer so a torn interleave is
	// detectable: a surviving file must match exactly one writer's value.
	maxFor := func(g int) uint64 { return uint64(1_000+g) * 1_000_000 }

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iterations)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if err := SaveStateFile(path, maxFor(g), float64(g), int64(g), uint64(g), g%2 == 0); err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent SaveStateFile: %v", err)
	}

	// File must be a complete state written by some single writer, not torn.
	max, ratio, addedAt, uploaded, _, ok := LoadStateFile(path)
	if !ok {
		t.Fatal("LoadStateFile returned ok=false after concurrent writes (torn file)")
	}
	g := int(ratio)
	if max != maxFor(g) || addedAt != int64(g) || uploaded != uint64(g) {
		t.Errorf("torn write: got max=%d ratio=%v addedAt=%d uploaded=%d, fields don't agree on one writer", max, ratio, addedAt, uploaded)
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
