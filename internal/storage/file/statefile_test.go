package file

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/jdel/emission/internal/model"
)

func TestStateRoundtrip(t *testing.T) {
	// Use a value the old "1.0 KB" human-readable format would have rounded
	// (locking in the lossless byte-count layout).
	path := filepath.Join(t.TempDir(), "x.torrent")
	const ts int64 = 1700000000000
	want := model.TorrentState{MaxSpeed: 1500000, MaxRatio: 2.5, AddedAt: ts, UploadedBytes: 123456, DeleteOnCap: true}
	if err := (States{}).Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, ok := States{}.Load(path)
	if !ok {
		t.Fatal("Load returned ok=false")
	}
	if got != want {
		t.Errorf("roundtrip = %+v, want %+v", got, want)
	}
}

func TestStateZeroRatioRoundtrip(t *testing.T) {
	// 0 ratio is the "unlimited" sentinel. Must survive Save+Load.
	path := filepath.Join(t.TempDir(), "x.torrent")
	if err := (States{}).Save(path, model.TorrentState{MaxSpeed: 1024}); err != nil {
		t.Fatal(err)
	}
	got, ok := States{}.Load(path)
	if !ok {
		t.Fatal("Load returned ok=false")
	}
	if got.MaxRatio != 0 {
		t.Errorf("ratio = %v, want 0", got.MaxRatio)
	}
}

// TestStateConcurrentSave hammers one path from many goroutines, as the
// per-tracker announce loops do for a multi-tracker torrent. With a shared temp
// name, one goroutine's rename removes the temp out from under another (rename
// ENOENT), and concurrent O_TRUNC writes interleave into a torn file. The
// unique-temp-name atomic write must leave every save error-free and the final
// file a complete, parseable state — never a torn one.
func TestStateConcurrentSave(t *testing.T) {
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
				st := model.TorrentState{MaxSpeed: maxFor(g), MaxRatio: float64(g), AddedAt: int64(g), UploadedBytes: uint64(g), DeleteOnCap: g%2 == 0}
				if err := (States{}).Save(path, st); err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Save: %v", err)
	}

	// File must be a complete state written by some single writer, not torn.
	got, ok := States{}.Load(path)
	if !ok {
		t.Fatal("Load returned ok=false after concurrent writes (torn file)")
	}
	g := int(got.MaxRatio)
	if got.MaxSpeed != maxFor(g) || got.AddedAt != int64(g) || got.UploadedBytes != uint64(g) {
		t.Errorf("torn write: got %+v, fields don't agree on one writer", got)
	}
}

func TestStateMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.torrent")
	if _, ok := (States{}).Load(path); ok {
		t.Error("missing state should return ok=false")
	}
}

func TestStateNegativeRatio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	if err := (States{}).Save(path, model.TorrentState{MaxSpeed: 1024, MaxRatio: -0.5}); err != nil {
		t.Fatal(err)
	}
	if _, ok := (States{}).Load(path); ok {
		t.Error("negative ratio should be rejected on Load")
	}
}
