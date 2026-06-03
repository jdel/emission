package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func (rl *rateLimiter) size() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.buckets)
}

func TestRateLimiterEvictsIdleKeys(t *testing.T) {
	rl := newRateLimiter(1, 10)
	rl.idleTTL = 10 * time.Millisecond
	rl.sweepEvery = 0 // sweep on every call

	for i := 0; i < 100; i++ {
		rl.allow(fmt.Sprintf("ip-%d", i))
	}
	if got := rl.size(); got != 100 {
		t.Fatalf("after 100 distinct keys: size=%d, want 100", got)
	}

	time.Sleep(15 * time.Millisecond) // let the 100 keys go idle past idleTTL
	rl.allow("trigger")               // this call's sweep should drop them

	if got := rl.size(); got != 1 {
		t.Errorf("idle keys not evicted: size=%d, want 1 (only %q)", got, "trigger")
	}
}

func TestRateLimiterAllowsBurst(t *testing.T) {
	rl := newRateLimiter(1, 5)
	for i := 0; i < 5; i++ {
		if !rl.allow("k") {
			t.Fatalf("request %d denied within burst", i+1)
		}
	}
}

func TestRateLimiterBlocksAfterBurst(t *testing.T) {
	rl := newRateLimiter(1, 3)
	for i := 0; i < 3; i++ {
		rl.allow("k")
	}
	if rl.allow("k") {
		t.Fatal("expected denial after burst exhausted")
	}
}

func TestRateLimiterIndependentKeys(t *testing.T) {
	rl := newRateLimiter(1, 1)
	if !rl.allow("a") {
		t.Fatal("a denied on first request")
	}
	if !rl.allow("b") {
		t.Fatal("b should be independent of a")
	}
}

func TestProxyTrustDirectPeer(t *testing.T) {
	pt := newProxyTrust(nil) // no trusted proxies
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "1.2.3.4:1234"
	r.Header.Set("X-Forwarded-For", "9.9.9.9")
	// XFF must be ignored when peer is not trusted
	if got := pt.clientIP(r); got != "1.2.3.4" {
		t.Errorf("clientIP = %q, want 1.2.3.4", got)
	}
}

func TestProxyTrustTrustedProxy(t *testing.T) {
	pt := newProxyTrust([]string{"10.0.0.0/8"})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "5.6.7.8")
	if got := pt.clientIP(r); got != "5.6.7.8" {
		t.Errorf("clientIP = %q, want 5.6.7.8", got)
	}
}

func TestProxyTrustSpoofedXFF(t *testing.T) {
	// Peer is not trusted — attacker sends a fake XFF, must be ignored.
	pt := newProxyTrust([]string{"10.0.0.0/8"})
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "1.2.3.4:1234"
	r.Header.Set("X-Forwarded-For", "9.9.9.9")
	if got := pt.clientIP(r); got != "1.2.3.4" {
		t.Errorf("clientIP = %q, want 1.2.3.4", got)
	}
}

func newTestRpsLimiter(rate, burst float64) *rpsLimiter {
	l := newRpsLimiter(newProxyTrust(nil))
	l.perIP = newRateLimiter(rate, burst)
	l.global = newRateLimiter(100, 100)
	return l
}

func TestLimitAuthThrottles(t *testing.T) {
	rl := newTestRpsLimiter(1, 2)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := rl.wrap(inner)

	pass, blocked := 0, 0
	for i := 0; i < 10; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/register/begin", nil)
		r.RemoteAddr = "1.2.3.4:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			blocked++
		} else {
			pass++
		}
	}
	if pass != 2 {
		t.Errorf("expected 2 passing (burst), got %d", pass)
	}
	if blocked != 8 {
		t.Errorf("expected 8 blocked, got %d", blocked)
	}
}

func TestLimitRpsBlocksOnExhaustedBucket(t *testing.T) {
	rl := newTestRpsLimiter(0, 0) // deny everything immediately
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := rl.wrap(inner)

	r := httptest.NewRequest(http.MethodPost, "/api/auth/invite", nil)
	r.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}
