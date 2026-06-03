package api

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdel/emission/internal/client"
	"github.com/jdel/emission/internal/seeder"
)

// stallProxy is a fake HTTP proxy that accepts a connection and then never
// replies, standing in for a proxy that hangs. Returns its "http://host:port"
// URL and a stop func.
func stallProxy(t *testing.T) (proxyURL string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go func(c net.Conn) {
				<-done // hold the connection open, never respond
				c.Close()
			}(conn)
		}
	}()
	return "http://" + ln.Addr().String(), func() { close(done); ln.Close() }
}

// TestSetProxyProbeIsBounded checks bug B's fix: the inline reachability probe
// is capped at proxyProbeTimeout (5s), not the old 10s. Pointed at a proxy that
// never responds, the handler must return in well under 10s.
func TestSetProxyProbeIsBounded(t *testing.T) {
	proxyURL, stop := stallProxy(t)
	defer stop()

	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	// Register proxyURL as the trusted server default so its loopback address
	// clears the SSRF guard and the probe actually dials our stalling fake.
	mgr := seeder.New(c, t.TempDir(), 0, false, 1<<20, proxyURL)
	t.Cleanup(mgr.Shutdown)
	srv := &server{mgr: mgr, torrentsDir: t.TempDir()} // auth nil → owner "admin"

	body := fmt.Sprintf(`{"proxy":%q}`, proxyURL)
	rec := httptest.NewRecorder()
	start := time.Now()
	srv.setProxy(rec, httptest.NewRequest(http.MethodPut, "/api/proxy", strings.NewReader(body)))
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT code %d body %s", rec.Code, rec.Body)
	}
	// Generous upper bound: 5s timeout + slack, but clearly below the old 10s.
	if elapsed > 7*time.Second {
		t.Fatalf("probe took %v; want bounded by the %v timeout", elapsed, 5*time.Second)
	}
	// Sanity: it did wait for the timeout (proves the probe ran and was capped).
	if elapsed < 4*time.Second {
		t.Fatalf("probe returned in %v; expected it to hit the ~5s timeout", elapsed)
	}
}

// TestSetProxyRouteIsRateLimited checks the other half of the fix: PUT /api/proxy
// is wrapped by the shared rate limiter (per-IP burst 10), so a client cannot
// spam probe-triggering requests. Uses an empty proxy body so each allowed
// request resolves to "direct" and returns without any outbound probe.
func TestSetProxyRouteIsRateLimited(t *testing.T) {
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	mgr := seeder.New(c, t.TempDir(), 0, false, 1<<20, "")
	t.Cleanup(mgr.Shutdown)
	srv := &server{mgr: mgr, torrentsDir: t.TempDir()}

	rl := newRpsLimiter(newProxyTrust(nil)) // per-IP burst 10
	mux := newMux(srv, false, rl)

	pass, blocked := 0, 0
	for i := 0; i < 12; i++ {
		r := httptest.NewRequest(http.MethodPut, "/api/proxy", strings.NewReader(`{"proxy":""}`))
		r.RemoteAddr = "1.2.3.4:1234"
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			blocked++
		} else {
			pass++
		}
	}
	if blocked == 0 {
		t.Fatalf("PUT /api/proxy never rate-limited over 12 requests (pass=%d); limiter not wired", pass)
	}
	if pass != 10 {
		t.Errorf("passing requests = %d, want 10 (per-IP burst)", pass)
	}
}
