package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a per-key token bucket. rate tokens are added per second up
// to burst. allow returns true when a token is consumed, false when the bucket
// is empty.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]float64
	last    map[string]time.Time
	rate    float64
	burst   float64
}

func newRateLimiter(rate, burst float64) *rateLimiter {
	return &rateLimiter{
		buckets: map[string]float64{},
		last:    map[string]time.Time{},
		rate:    rate,
		burst:   burst,
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if t, ok := rl.last[key]; ok {
		rl.buckets[key] += now.Sub(t).Seconds() * rl.rate // refill tokens earned since last call
		if rl.buckets[key] > rl.burst {
			rl.buckets[key] = rl.burst // cap at burst ceiling
		}
	} else {
		rl.buckets[key] = rl.burst // first seen: start full
	}
	rl.last[key] = now
	if rl.buckets[key] < 1 {
		return false // bucket empty: deny
	}
	rl.buckets[key]-- // consume one token
	return true
}

// proxyTrust resolves the real client IP from a request. It trusts
// X-Forwarded-For only when the immediate peer is within a configured CIDR
// set, and then walks the XFF chain right-to-left past trusted hops. This
// defeats XFF spoofing from untrusted peers while working correctly behind a
// single reverse proxy (e.g. Traefik in the bundled compose).
type proxyTrust struct{ nets []*net.IPNet }

func newProxyTrust(cidrs []string) *proxyTrust {
	pt := &proxyTrust{}
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(c); err == nil {
			pt.nets = append(pt.nets, n)
		}
	}
	return pt
}

func (pt *proxyTrust) trusted(ip net.IP) bool {
	for _, n := range pt.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (pt *proxyTrust) clientIP(r *http.Request) string {
	peerStr, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peerStr = r.RemoteAddr
	}
	peer := net.ParseIP(peerStr)

	if peer == nil || !pt.trusted(peer) {
		return peerStr
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return peerStr
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil || pt.trusted(ip) {
			continue
		}
		return ip.String()
	}
	return peerStr
}

// rpsLimiter wraps handlers with shared per-client and global token buckets.
// Construct once and apply to each route via wrap — all wrapped routes share
// the same buckets so the limits are enforced across routes, not per-route.
type rpsLimiter struct {
	pt     *proxyTrust
	perIP  *rateLimiter
	global *rateLimiter
}

func newRpsLimiter(pt *proxyTrust) *rpsLimiter {
	return &rpsLimiter{
		pt:     pt,
		perIP:  newRateLimiter(1, 10),   // ~1 req/s sustained, burst 10, per client
		global: newRateLimiter(20, 100), // backstop: ≤20/s across all limited routes
	}
}

func (l *rpsLimiter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.perIP.allow(l.pt.clientIP(r)) || !l.global.allow("rps") {
			writeError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}
