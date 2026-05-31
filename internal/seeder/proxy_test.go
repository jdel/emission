package seeder

import (
	"context"
	"net/http"
	"testing"
)

func TestValidateProxyURL(t *testing.T) {
	ok := []string{
		"http://1.2.3.4:8080",
		"https://proxy.example.com:3128",
		"socks5://10.0.0.1:1080", // format ok; local-IP policy is separate
	}
	for _, s := range ok {
		if err := ValidateProxyURL(s); err != nil {
			t.Errorf("ValidateProxyURL(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{
		"",                              // empty (callers treat as direct before calling)
		"socks4://1.2.3.4:1080",         // unsupported scheme
		"ftp://1.2.3.4:21",              // unsupported scheme
		"http://1.2.3.4",                // no port
		"http://1.2.3.4:99999",          // port out of range
		"http://1.2.3.4:abc",            // non-numeric port
		"http://user:pass@1.2.3.4:8080", // userinfo (exfil vector)
		"http://1.2.3.4:8080/path",      // path
		"http://1.2.3.4:8080?q=1",       // query
		"http://1.2.3.4:8080#f",         // fragment
	}
	for _, s := range bad {
		if err := ValidateProxyURL(s); err == nil {
			t.Errorf("ValidateProxyURL(%q) = nil, want error", s)
		}
	}
}

func TestUserProxyDefaultAndOverride(t *testing.T) {
	c := newTestClient(t)
	m := New(c, t.TempDir(), 0, false, 1<<20, "http://default.example:8080")
	t.Cleanup(m.Shutdown)

	// Unset → inherits the server default, not explicit.
	if px, explicit := m.UserProxy("alice"); px != "http://default.example:8080" || explicit {
		t.Errorf("default: got (%q, %v), want (default, false)", px, explicit)
	}
	// Override with a public proxy.
	if err := m.SetUserProxy("alice", "http://other.example:3128"); err != nil {
		t.Fatal(err)
	}
	if px, explicit := m.UserProxy("alice"); px != "http://other.example:3128" || !explicit {
		t.Errorf("override: got (%q, %v), want (other, true)", px, explicit)
	}
	// Explicit empty → direct.
	if err := m.SetUserProxy("alice", ""); err != nil {
		t.Fatal(err)
	}
	if px, explicit := m.UserProxy("alice"); px != "" || !explicit {
		t.Errorf("direct: got (%q, %v), want (\"\", true)", px, explicit)
	}
}

func TestSetUserProxyRejectsLocal(t *testing.T) {
	c := newTestClient(t)
	// Default is a local address — admin is trusted, so users may adopt it.
	m := New(c, t.TempDir(), 0, false, 1<<20, "socks5://10.0.0.1:1080")
	t.Cleanup(m.Shutdown)

	local := []string{
		"http://127.0.0.1:8080",
		"http://192.168.1.1:8080",
		"http://172.16.0.5:3128",
		"socks5://10.1.2.3:1080",
	}
	for _, s := range local {
		if err := m.SetUserProxy("alice", s); err == nil {
			t.Errorf("SetUserProxy(%q) = nil, want rejection of local address", s)
		}
	}
	// The trusted CLI default may be adopted even though it is local.
	if err := m.SetUserProxy("alice", "socks5://10.0.0.1:1080"); err != nil {
		t.Errorf("adopting trusted default: %v, want nil", err)
	}
	// A public address is fine.
	if err := m.SetUserProxy("bob", "http://proxy.example.com:8080"); err != nil {
		t.Errorf("public proxy: %v, want nil", err)
	}
}

func TestAnnounceClientTrustGuard(t *testing.T) {
	c := newTestClient(t)
	m := New(c, t.TempDir(), 0, false, 1<<20, "http://default.example:8080")
	t.Cleanup(m.Shutdown)

	// Direct owner → shared no-proxy client (no transport).
	if err := m.SetUserProxy("d", ""); err != nil {
		t.Fatal(err)
	}
	if got := m.announceClient("d"); got.Transport != nil {
		t.Error("direct owner should use the no-proxy client")
	}

	// Inherited default → trusted, dialed without the internal-address guard.
	def := m.announceClient("inheritor").Transport.(*http.Transport)
	if def.Proxy == nil {
		t.Error("default proxy client should set Transport.Proxy")
	}
	if def.DialContext != nil {
		t.Error("trusted default must not carry the internal-address guard")
	}

	// User-supplied → guarded.
	if err := m.SetUserProxy("u", "http://other.example:3128"); err != nil {
		t.Fatal(err)
	}
	user := m.announceClient("u").Transport.(*http.Transport)
	if user.DialContext == nil {
		t.Error("user-supplied proxy must be dialed through the guard")
	}
}

func TestProxyClientCachePerOwner(t *testing.T) {
	c := newTestClient(t)
	m := New(c, t.TempDir(), 0, false, 1<<20, "")
	t.Cleanup(m.Shutdown)

	// Announcing caches a client keyed by owner.
	if err := m.SetUserProxy("u", "http://a.example:8080"); err != nil {
		t.Fatal(err)
	}
	_ = m.announceClient("u")
	if _, ok := m.proxyClients["u"]; !ok {
		t.Fatal("expected a cached client for u")
	}

	// Changing u's proxy drops u's client; it rebuilds on the next announce.
	if err := m.SetUserProxy("u", "http://b.example:3128"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.proxyClients["u"]; ok {
		t.Error("u's stale client should be evicted on change")
	}

	// Owners are independent even on the same proxy URL: changing one does not
	// touch another's client.
	if err := m.SetUserProxy("u", "http://a.example:8080"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetUserProxy("v", "http://a.example:8080"); err != nil {
		t.Fatal(err)
	}
	_ = m.announceClient("u")
	_ = m.announceClient("v")
	if err := m.SetUserProxy("u", ""); err != nil { // u goes direct
		t.Fatal(err)
	}
	if _, ok := m.proxyClients["u"]; ok {
		t.Error("u's client should be evicted")
	}
	if _, ok := m.proxyClients["v"]; !ok {
		t.Error("changing u must not affect v's client")
	}
}

func TestProxyStatus(t *testing.T) {
	c := newTestClient(t)
	m := New(c, t.TempDir(), 0, false, 1<<20, "")
	t.Cleanup(m.Shutdown)

	// No proxy → direct.
	if st, _ := m.ProxyStatus("alice"); st != "direct" {
		t.Errorf("no proxy: status %q, want direct", st)
	}
	// Set but not probed → unknown.
	if err := m.SetUserProxy("alice", "http://proxy.example.com:8080"); err != nil {
		t.Fatal(err)
	}
	if st, _ := m.ProxyStatus("alice"); st != "unknown" {
		t.Errorf("unprobed: status %q, want unknown", st)
	}
	// Probe an unreachable proxy → error with a message. ".invalid" never
	// resolves (RFC 6761), so the probe fails without touching the network.
	if err := m.SetUserProxy("bob", "http://proxy.invalid:9"); err != nil {
		t.Fatal(err)
	}
	ok, msg := m.ProbeUserProxy(context.Background(), "bob")
	if ok || msg == "" {
		t.Errorf("probe of dead proxy: ok=%v msg=%q, want failure with message", ok, msg)
	}
	if st, errMsg := m.ProxyStatus("bob"); st != "error" || errMsg == "" {
		t.Errorf("after failed probe: status %q err %q, want error+msg", st, errMsg)
	}
}
