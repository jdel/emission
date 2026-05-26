package seeder

import (
	"bufio"
	"encoding/json"
	"os"
)

// Stats are stored as JSON lines (one StatsPoint object per line) so appends
// are O(new points) rather than O(total history).

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

// appendStatsFile writes pts as new JSON lines at the end of path.
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
		if _, err := w.Write(line); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	return w.Flush()
}

func removeStatsFile(torrentPath string) {
	_ = os.Remove(torrentPath + ".stats")
}
