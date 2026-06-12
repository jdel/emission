package seeder

import (
	"path/filepath"
	"testing"

	"github.com/jdel/emission/internal/storage/file"
)

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
	onDisk, err := file.Stats{}.Load(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(onDisk) != statsMaxPoints {
		t.Errorf("on-disk len = %d, want capped %d", len(onDisk), statsMaxPoints)
	}
}

// TestStatsFileCompacts drives many append+flush cycles whose cumulative writes
// would, without compaction, far exceed statsCompactAt; the history must stay
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
	onDisk, err := file.Stats{}.Load(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(onDisk) > statsCompactAt {
		t.Errorf("history not bounded: %d lines > statsCompactAt %d", len(onDisk), statsCompactAt)
	}
	if len(onDisk) >= total {
		t.Errorf("no compaction: %d lines after appending %d", len(onDisk), total)
	}
}
