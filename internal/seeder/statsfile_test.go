package seeder

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatsFileRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	pts := []StatsPoint{
		{TimeMs: 1000, Rate: 512, Leechers: 3},
		{TimeMs: 2000, Rate: 1024, Leechers: 7},
	}
	if err := appendStatsFile(path+".stats", pts); err != nil {
		t.Fatal(err)
	}
	got, err := loadStatsFile(path + ".stats")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(pts) {
		t.Fatalf("len = %d, want %d", len(got), len(pts))
	}
	for i, p := range pts {
		if got[i] != p {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], p)
		}
	}
}

func TestStatsFileAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent.stats")
	first := []StatsPoint{{TimeMs: 1, Rate: 100, Leechers: 1}}
	second := []StatsPoint{{TimeMs: 2, Rate: 200, Leechers: 2}}
	if err := appendStatsFile(path, first); err != nil {
		t.Fatal(err)
	}
	if err := appendStatsFile(path, second); err != nil {
		t.Fatal(err)
	}
	got, err := loadStatsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0] != first[0] || got[1] != second[0] {
		t.Errorf("points mismatch: %+v", got)
	}
}

func TestStatsFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.stats")
	if _, err := loadStatsFile(path); err == nil {
		t.Error("missing file should return error")
	}
}

func TestStatsFileSkipsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.stats")
	good := []StatsPoint{{TimeMs: 42, Rate: 99, Leechers: 1}}
	if err := appendStatsFile(path, good); err != nil {
		t.Fatal(err)
	}
	// Append a malformed line directly.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("not-json\n")
	_ = f.Close()

	got, err := loadStatsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != good[0] {
		t.Errorf("got %+v, want 1 good point", got)
	}
}

func TestStatsFileEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.stats")
	if err := appendStatsFile(path, nil); err != nil {
		t.Fatal(err)
	}
	got, err := loadStatsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}
