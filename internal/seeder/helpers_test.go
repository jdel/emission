package seeder

import (
	"testing"
	"time"

	"github.com/jdel/emission/internal/tracker"
)

func TestUploadCapFor(t *testing.T) {
	cases := []struct {
		size  uint64
		ratio float64
		want  uint64
	}{
		{1024, 0, 0},      // 0 ratio → unlimited
		{1024, 1.0, 1024}, // exact size
		{1024, 2.5, 2560},
		{0, 2.0, 0},     // 0 size → unlimited
		{1024, -1.0, 0}, // negative ratio → unlimited (defensive)
	}
	for _, c := range cases {
		if got := uploadCapFor(c.size, c.ratio); got != c.want {
			t.Errorf("uploadCapFor(%d, %v) = %d, want %d", c.size, c.ratio, got, c.want)
		}
	}
}

func TestPickRateWeighted(t *testing.T) {
	const k = 3.33
	// equal bounds → always returns min regardless of leechers
	if got := pickRateWeighted(100, 100, 0, k); got != 100 {
		t.Errorf("equal bounds (0 leechers): got %d, want 100", got)
	}
	if got := pickRateWeighted(100, 100, 10, k); got != 100 {
		t.Errorf("equal bounds (10 leechers): got %d, want 100", got)
	}
	// result always stays within [min, max]
	for i := 0; i < 200; i++ {
		for _, leechers := range []int64{0, 1, 10, 1000} {
			got := pickRateWeighted(100, 200, leechers, k)
			if got < 100 || got > 200 {
				t.Errorf("leechers=%d: out of range [100,200]: %d", leechers, got)
			}
		}
	}
	// negative leechers treated as 0 — no panic
	_ = pickRateWeighted(100, 200, -5, k)
}

func TestBackoff(t *testing.T) {
	cases := []struct {
		in, want time.Duration
	}{
		{time.Second, 30 * time.Second},      // raised to floor
		{time.Minute, 2 * time.Minute},       // doubled
		{20 * time.Minute, 30 * time.Minute}, // capped
		{2 * time.Hour, 30 * time.Minute},    // way above cap
	}
	for _, c := range cases {
		if got := backoff(c.in); got != c.want {
			t.Errorf("backoff(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestClampMin(t *testing.T) {
	if got := clampMin(time.Second, 5*time.Second); got != 5*time.Second {
		t.Errorf("below floor: got %s, want 5s", got)
	}
	if got := clampMin(10*time.Second, 5*time.Second); got != 10*time.Second {
		t.Errorf("above floor: got %s, want 10s", got)
	}
	if got := clampMin(time.Second, 0); got != time.Second {
		t.Errorf("zero floor disables clamp: got %s, want 1s", got)
	}
}

func TestPickIntervalFallback(t *testing.T) {
	// Error → fallback
	if got := pickInterval(nil, errFake, 5*time.Minute); got != 5*time.Minute {
		t.Errorf("err path: got %s", got)
	}
	// nil response → fallback
	if got := pickInterval(nil, nil, 5*time.Minute); got != 5*time.Minute {
		t.Errorf("nil resp: got %s", got)
	}
	// resp interval zero → fallback
	resp := &tracker.Response{Interval: 0}
	if got := pickInterval(resp, nil, 5*time.Minute); got != 5*time.Minute {
		t.Errorf("zero interval: got %s", got)
	}
	// resp interval set → used as-is
	resp = &tracker.Response{Interval: 10 * time.Minute}
	if got := pickInterval(resp, nil, 5*time.Minute); got != 10*time.Minute {
		t.Errorf("normal: got %s", got)
	}
	// resp min interval > interval → min wins
	resp = &tracker.Response{Interval: 5 * time.Minute, MinInterval: 8 * time.Minute}
	if got := pickInterval(resp, nil, time.Minute); got != 8*time.Minute {
		t.Errorf("min wins: got %s", got)
	}
}

// errFake is a sentinel for table-driven tests where the value of err is not
// inspected, only its non-nilness.
var errFake = &sentinelError{}

type sentinelError struct{}

func (*sentinelError) Error() string { return "sentinel" }
