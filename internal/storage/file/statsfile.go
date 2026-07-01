package file

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"

	"github.com/jdel/emission/internal/model"
)

// Stats persists rate history as a <name>.torrent.stats sidecar of JSON lines
// (one StatsPoint per line). New points are appended cheaply; the caller
// bounds the file by calling Rewrite (an atomic replace) when it grows past
// its window.
type Stats struct{}

func statsPath(torrentPath string) string { return torrentPath + ".stats" }

// Load reads the stored history in append order, skipping any malformed
// trailing line left by a crash mid-write.
func (Stats) Load(torrentPath string) ([]model.StatsPoint, error) {
	f, err := os.Open(statsPath(torrentPath))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var pts []model.StatsPoint
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var pt model.StatsPoint
		if json.Unmarshal(sc.Bytes(), &pt) == nil {
			pts = append(pts, pt)
		}
	}
	return pts, sc.Err()
}

// Append adds pts as JSON lines (creating the file). A crash mid-write may
// leave a partial trailing line, which Load skips; the next Rewrite is atomic.
func (Stats) Append(torrentPath string, pts []model.StatsPoint) error {
	f, err := os.OpenFile(statsPath(torrentPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
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

// Rewrite atomically replaces the whole history with pts (compaction).
func (Stats) Rewrite(torrentPath string, pts []model.StatsPoint) error {
	var b bytes.Buffer
	for _, pt := range pts {
		line, err := json.Marshal(pt)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return atomicWrite(statsPath(torrentPath), b.Bytes(), 0o644)
}

// Delete removes the sidecar, ignoring not-exist errors.
func (Stats) Delete(torrentPath string) {
	_ = os.Remove(statsPath(torrentPath))
}
