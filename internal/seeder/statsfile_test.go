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
	if err := writeStatsFile(path+".stats", pts); err != nil {
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

func TestStatsFileOverwrites(t *testing.T) {
	// writeStatsFile replaces the file (mirrors the capped buffer), it does not
	// append — a second write must fully supersede the first.
	path := filepath.Join(t.TempDir(), "x.torrent.stats")
	first := []StatsPoint{{TimeMs: 1, Rate: 100, Leechers: 1}}
	second := []StatsPoint{{TimeMs: 2, Rate: 200, Leechers: 2}, {TimeMs: 3, Rate: 300, Leechers: 3}}
	if err := writeStatsFile(path, first); err != nil {
		t.Fatal(err)
	}
	if err := writeStatsFile(path, second); err != nil {
		t.Fatal(err)
	}
	got, err := loadStatsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != second[0] || got[1] != second[1] {
		t.Errorf("got %+v, want only the second write %+v", got, second)
	}
}

func TestStatsFileAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.stats")
	if err := appendStatsFile(path, []StatsPoint{{TimeMs: 1, Rate: 100, Leechers: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := appendStatsFile(path, []StatsPoint{{TimeMs: 2, Rate: 200, Leechers: 2}}); err != nil {
		t.Fatal(err)
	}
	got, err := loadStatsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TimeMs != 1 || got[1].TimeMs != 2 {
		t.Errorf("append should accumulate: got %+v", got)
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
	if err := writeStatsFile(path, good); err != nil {
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
	if err := writeStatsFile(path, nil); err != nil {
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

// TestStatsCappedInMemoryAndOnDisk verifies the retention cap: appending past
// statsMaxPoints drops the oldest in memory, and flushStats mirrors that capped
// buffer to disk (no unbounded growth).
func TestStatsCappedInMemoryAndOnDisk(t *testing.T) {
	m := newTestManager(t, t.TempDir())
	s := &session{id: "x", mgr: m, path: filepath.Join(t.TempDir(), "x.torrent")}

	for i := 0; i < statsMaxPoints+50; i++ {
		s.appendStat(int64(i)) // Leechers = i, so we can spot the oldest retained
	}
	if len(s.statsBuf) != statsMaxPoints {
		t.Fatalf("buffer len = %d, want cap %d", len(s.statsBuf), statsMaxPoints)
	}
	if s.statsBuf[0].Leechers != 50 {
		t.Errorf("oldest not evicted: first leechers = %d, want 50", s.statsBuf[0].Leechers)
	}

	s.flushStats()
	onDisk, err := loadStatsFile(s.path + ".stats")
	if err != nil {
		t.Fatal(err)
	}
	if len(onDisk) != statsMaxPoints {
		t.Errorf("on-disk len = %d, want capped %d", len(onDisk), statsMaxPoints)
	}
}

// TestStatsFileCompacts drives many append+flush cycles whose cumulative writes
// would, without compaction, far exceed statsCompactAt; the file must stay
// bounded (compaction kicked in) rather than growing to the total appended.
func TestStatsFileCompacts(t *testing.T) {
	m := newTestManager(t, t.TempDir())
	s := &session{id: "x", mgr: m, path: filepath.Join(t.TempDir(), "x.torrent")}

	total := statsCompactAt + 5000
	for written := 0; written < total; written += 1000 {
		for i := 0; i < 1000; i++ {
			s.appendStat(int64(written + i))
		}
		s.flushStats()
	}
	onDisk, err := loadStatsFile(s.path + ".stats")
	if err != nil {
		t.Fatal(err)
	}
	if len(onDisk) > statsCompactAt {
		t.Errorf("file not bounded: %d lines > statsCompactAt %d", len(onDisk), statsCompactAt)
	}
	if len(onDisk) >= total {
		t.Errorf("no compaction: %d lines after appending %d", len(onDisk), total)
	}
}
