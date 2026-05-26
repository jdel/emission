package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestLimitAuthThrottles(t *testing.T) {
	pt := newProxyTrust(nil)
	perIP := newRateLimiter(1, 2)
	global := newRateLimiter(100, 100)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := limitAuth(pt, perIP, global, inner)

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

func TestLimitAuthSkipsNonPublicPaths(t *testing.T) {
	pt := newProxyTrust(nil)
	perIP := newRateLimiter(0, 0) // deny everything
	global := newRateLimiter(0, 0)

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := limitAuth(pt, perIP, global, inner)

	// /api/torrents is not in publicAPIPaths — limiter must not apply
	r := httptest.NewRequest(http.MethodGet, "/api/torrents", nil)
	r.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("non-public path should not be rate-limited, got %d", w.Code)
	}
}
