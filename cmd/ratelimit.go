package cmd

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
		rl.buckets[key] += now.Sub(t).Seconds() * rl.rate
		if rl.buckets[key] > rl.burst {
			rl.buckets[key] = rl.burst
		}
	} else {
		rl.buckets[key] = rl.burst
	}
	rl.last[key] = now
	if rl.buckets[key] < 1 {
		return false
	}
	rl.buckets[key]--
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

// limitAuth throttles requests to the unauthenticated auth routes. It applies
// both a per-client bucket and a global backstop. The global bucket bounds
// total unauthenticated auth attempts regardless of source, closing the
// IPv6-rotation gap where an attacker with a /64 can cycle per-IP buckets.
func limitAuth(pt *proxyTrust, perIP, global *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicAPIPaths[r.URL.Path] {
			if !perIP.allow(pt.clientIP(r)) || !global.allow("auth") {
				writeError(w, http.StatusTooManyRequests, "too many requests")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
