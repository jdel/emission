package seeder

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
)

// Stats are stored as JSON lines (one StatsPoint per line). New points are
// appended cheaply (O(new)); once the file would exceed statsCompactAt lines it
// is atomically rewritten down to the retained window, so it stays bounded
// without paying a full rewrite on every flush.

func loadStatsFile(path string) ([]StatsPoint, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var pts []StatsPoint
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var pt StatsPoint
		if json.Unmarshal(sc.Bytes(), &pt) == nil {
			pts = append(pts, pt)
		}
	}
	return pts, sc.Err()
}

// writeStatsFile atomically replaces path with pts as JSON lines (compaction).
func writeStatsFile(path string, pts []StatsPoint) error {
	var b bytes.Buffer
	for _, pt := range pts {
		line, err := json.Marshal(pt)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return atomicWrite(path, b.Bytes(), 0o644)
}

// appendStatsFile appends pts as JSON lines to path (creating it). Cheap O(new),
// used between compactions. A crash mid-write may leave a partial trailing line,
// which loadStatsFile skips; the next compaction (writeStatsFile) is atomic.
func appendStatsFile(path string, pts []StatsPoint) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, pt := range pts {
		line, err := json.Marshal(pt)
		if err != nil {
			return err
		}
		w.Write(line)
		w.WriteByte('\n')
	}
	return w.Flush()
}

func removeStatsFile(torrentPath string) {
	_ = os.Remove(torrentPath + ".stats")
}
