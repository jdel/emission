package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jdel/emission/internal/model"
)

func TestStatsRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	pts := []model.StatsPoint{
		{TimeMs: 1000, Rate: 512, Leechers: 3},
		{TimeMs: 2000, Rate: 1024, Leechers: 7},
	}
	if err := (Stats{}).Rewrite(path, pts); err != nil {
		t.Fatal(err)
	}
	got, err := Stats{}.Load(path)
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

func TestStatsRewriteOverwrites(t *testing.T) {
	// Rewrite replaces the history (mirrors the capped buffer), it does not
	// append — a second write must fully supersede the first.
	path := filepath.Join(t.TempDir(), "x.torrent")
	first := []model.StatsPoint{{TimeMs: 1, Rate: 100, Leechers: 1}}
	second := []model.StatsPoint{{TimeMs: 2, Rate: 200, Leechers: 2}, {TimeMs: 3, Rate: 300, Leechers: 3}}
	if err := (Stats{}).Rewrite(path, first); err != nil {
		t.Fatal(err)
	}
	if err := (Stats{}).Rewrite(path, second); err != nil {
		t.Fatal(err)
	}
	got, err := Stats{}.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != second[0] || got[1] != second[1] {
		t.Errorf("got %+v, want only the second write %+v", got, second)
	}
}

func TestStatsAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	if err := (Stats{}).Append(path, []model.StatsPoint{{TimeMs: 1, Rate: 100, Leechers: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := (Stats{}).Append(path, []model.StatsPoint{{TimeMs: 2, Rate: 200, Leechers: 2}}); err != nil {
		t.Fatal(err)
	}
	got, err := Stats{}.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TimeMs != 1 || got[1].TimeMs != 2 {
		t.Errorf("append should accumulate: got %+v", got)
	}
}

func TestStatsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.torrent")
	if _, err := (Stats{}).Load(path); err == nil {
		t.Error("missing history should return error")
	}
}

func TestStatsSkipsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.torrent")
	good := []model.StatsPoint{{TimeMs: 42, Rate: 99, Leechers: 1}}
	if err := (Stats{}).Rewrite(path, good); err != nil {
		t.Fatal(err)
	}
	// Append a malformed line directly to the backing file.
	f, err := os.OpenFile(path+".stats", os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("not-json\n")
	_ = f.Close()

	got, err := Stats{}.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != good[0] {
		t.Errorf("got %+v, want 1 good point", got)
	}
}

func TestStatsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.torrent")
	if err := (Stats{}).Rewrite(path, nil); err != nil {
		t.Fatal(err)
	}
	got, err := Stats{}.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}
