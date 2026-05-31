package cmd

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jdel/emission/internal/client"
	"github.com/jdel/emission/internal/seeder"
)

func TestSafeTargetPath(t *testing.T) {
	dir := t.TempDir()
	s := &server{torrentsDir: dir}

	cases := []struct {
		name       string
		username   string
		filename   string
		wantErr    bool
		wantSuffix string // checked when wantErr is false; relative to dir
	}{
		{"root happy", "", "ok.torrent", false, "ok.torrent"},
		{"user happy", "alice", "ok.torrent", false, filepath.Join("alice", "ok.torrent")},
		{"strips path prefix", "", "../escape.torrent", false, "escape.torrent"},
		{"strips dir component", "", "/etc/passwd.torrent", false, "passwd.torrent"},
		{"appends .torrent suffix", "", "no-suffix", false, "no-suffix.torrent"},
		{"reject ..", "", "..", true, ""},
		{"reject .", "", ".", true, ""},
		{"reject empty", "", "", true, ""},
		{"reject only whitespace", "", "   ", true, ""},
		{"reject bad username", "bad-user", "ok.torrent", true, ""},
		{"reject username with separator", "ali/ce", "ok.torrent", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := s.safeTargetPath(c.username, c.filename)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if c.wantErr {
				return
			}
			want := filepath.Join(dir, c.wantSuffix)
			if got != want {
				t.Errorf("path = %q, want %q", got, want)
			}
			// Critical invariant: result is always under dir.
			rel, relErr := filepath.Rel(dir, got)
			if relErr != nil || strings.HasPrefix(rel, "..") {
				t.Errorf("escape detected: %q (rel %q)", got, rel)
			}
		})
	}
}

func TestBandwidthEndpoints(t *testing.T) {
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	mgr := seeder.New(c, t.TempDir(), 0, false, 1<<20, "") // 1M default
	t.Cleanup(mgr.Shutdown)
	srv := &server{mgr: mgr, torrentsDir: t.TempDir()} // auth nil → owner ""

	// GET own → default.
	rec := httptest.NewRecorder()
	srv.getBandwidth(rec, httptest.NewRequest(http.MethodGet, "/api/bandwidth", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET code %d", rec.Code)
	}
	var info bandwidthInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Bandwidth != 1<<20 || info.Default != 1<<20 {
		t.Errorf("info = %+v, want bandwidth/default 1<<20", info)
	}

	if info.Profile != "normal" {
		t.Errorf("default profile = %q, want normal", info.Profile)
	}

	// PUT own → 2M + aggressive curve (halfSaturation 1).
	rec = httptest.NewRecorder()
	srv.setMyBandwidth(rec, httptest.NewRequest(http.MethodPut, "/api/bandwidth", strings.NewReader(`{"bandwidth":"2M","halfSaturation":1}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT code %d body %s", rec.Code, rec.Body)
	}
	if got := mgr.Bandwidth(""); got != 2<<20 {
		t.Errorf("after PUT, bandwidth = %d, want 2<<20", got)
	}
	if got := mgr.HalfSaturation(""); got != 1 {
		t.Errorf("after PUT, halfSaturation = %v, want 1", got)
	}
	if got := mgr.Profile(""); got != "aggressive" {
		t.Errorf("after PUT, profile = %q, want aggressive", got)
	}

	// PUT invalid value → 400.
	rec = httptest.NewRecorder()
	srv.setMyBandwidth(rec, httptest.NewRequest(http.MethodPut, "/api/bandwidth", strings.NewReader(`{"bandwidth":"abc"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad value code %d, want 400", rec.Code)
	}

	// PUT out-of-range half-saturation → 400.
	rec = httptest.NewRecorder()
	srv.setMyBandwidth(rec, httptest.NewRequest(http.MethodPut, "/api/bandwidth", strings.NewReader(`{"bandwidth":"2M","halfSaturation":99}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad half-saturation code %d, want 400", rec.Code)
	}
}

func TestProxyEndpoints(t *testing.T) {
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	mgr := seeder.New(c, t.TempDir(), 0, false, 1<<20, "") // no default proxy
	t.Cleanup(mgr.Shutdown)
	srv := &server{mgr: mgr, torrentsDir: t.TempDir()} // auth nil → owner ""

	get := func() proxyInfo {
		rec := httptest.NewRecorder()
		srv.getProxy(rec, httptest.NewRequest(http.MethodGet, "/api/proxy", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET code %d", rec.Code)
		}
		var pi proxyInfo
		if err := json.Unmarshal(rec.Body.Bytes(), &pi); err != nil {
			t.Fatal(err)
		}
		return pi
	}
	put := func(body string) (int, proxyInfo) {
		rec := httptest.NewRecorder()
		srv.setProxy(rec, httptest.NewRequest(http.MethodPut, "/api/proxy", strings.NewReader(body)))
		var pi proxyInfo
		_ = json.Unmarshal(rec.Body.Bytes(), &pi)
		return rec.Code, pi
	}

	// No proxy set → direct.
	if pi := get(); pi.Status != "direct" || pi.Proxy != "" {
		t.Errorf("initial = %+v, want direct/empty", pi)
	}
	// Malformed scheme → 400.
	if code, _ := put(`{"proxy":"ftp://x:21"}`); code != http.StatusBadRequest {
		t.Errorf("malformed code %d, want 400", code)
	}
	// Local/private address → 400 (exfil/SSRF guard).
	if code, _ := put(`{"proxy":"http://127.0.0.1:8080"}`); code != http.StatusBadRequest {
		t.Errorf("local code %d, want 400", code)
	}
	// Well-formed but unreachable → 200, saved, status "error".
	code, pi := put(`{"proxy":"http://proxy.invalid:9"}`)
	if code != http.StatusOK {
		t.Fatalf("valid put code %d body %+v", code, pi)
	}
	if pi.Proxy != "http://proxy.invalid:9" || pi.Status != "error" || pi.Error == "" {
		t.Errorf("after set = %+v, want proxy set with error status", pi)
	}
	// Clear → direct.
	if code, pi := put(`{"proxy":""}`); code != http.StatusOK || pi.Status != "direct" {
		t.Errorf("clear = %d %+v, want 200/direct", code, pi)
	}
}

func TestQueryInt(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?limit=42&bad=oops", nil)
	if got := queryInt(r, "limit", 0); got != 42 {
		t.Errorf("limit = %d, want 42", got)
	}
	if got := queryInt(r, "bad", 7); got != 7 {
		t.Errorf("malformed → default: got %d, want 7", got)
	}
	if got := queryInt(r, "missing", 7); got != 7 {
		t.Errorf("missing → default: got %d, want 7", got)
	}
}

// buildMultipart builds a multipart request body with the given form fields.
func buildMultipart(t *testing.T, fields map[string]string) (*http.Request, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	r := httptest.NewRequest(http.MethodPost, "/x", &buf)
	r.Header.Set("Content-Type", w.FormDataContentType())
	return r, w.Boundary()
}

func TestParseSpeedFormDefaults(t *testing.T) {
	r, _ := buildMultipart(t, nil)
	max, ratio, override, err := parseSpeedForm(r, 500)
	if err != nil {
		t.Fatal(err)
	}
	if max != 500 || ratio != 0 {
		t.Errorf("got max=%d ratio=%v", max, ratio)
	}
	if override {
		t.Error("override should be false when no fields supplied")
	}
}

func TestParseSpeedFormOverride(t *testing.T) {
	r, _ := buildMultipart(t, map[string]string{
		"max-speed": "1M",
		"max-ratio": "2.5",
	})
	max, ratio, override, err := parseSpeedForm(r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if max != 1<<20 {
		t.Errorf("max = %d", max)
	}
	if ratio != 2.5 {
		t.Errorf("ratio = %v", ratio)
	}
	if !override {
		t.Error("override should be true")
	}
}

func TestWsOrigins(t *testing.T) {
	loopback := []string{"localhost:*", "127.0.0.1:*", "[::1]:*"}

	// Empty publicURL → loopback only (fail safe), regardless of auth mode.
	for _, authEnabled := range []bool{true, false} {
		got := wsOrigins("", authEnabled)
		if len(got) != len(loopback) {
			t.Errorf("empty publicURL (auth=%v): got %v", authEnabled, got)
		}
	}

	// Auth ON: public URL parsed as canonical base URL.
	got := wsOrigins("https://emission.example.com:8443", true)
	want := []string{"localhost:*", "127.0.0.1:*", "[::1]:*", "emission.example.com:8443"}
	if len(got) != len(want) {
		t.Fatalf("auth on: got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}

	// Auth ON: malformed URL → loopback only, no bad host leaked.
	got = wsOrigins("://bad", true)
	for _, p := range got {
		if strings.Contains(p, "bad") {
			t.Errorf("malformed URL leaked into patterns: %v", got)
		}
	}

	// Auth OFF: publicURL treated as raw glob list.
	got = wsOrigins("10.0.0.*:8080,*.lan:8080", false)
	wantSuffixes := []string{"10.0.0.*:8080", "*.lan:8080"}
	for _, w := range wantSuffixes {
		found := false
		for _, p := range got {
			if p == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("auth off: pattern %q missing from %v", w, got)
		}
	}

	// Auth OFF: bare "*" is dropped.
	got = wsOrigins("*", false)
	for _, p := range got {
		if p == "*" {
			t.Errorf("auth off: bare wildcard must be dropped, got %v", got)
		}
	}

	// Auth OFF: scheme prefix stripped when pasted as full URL.
	got = wsOrigins("http://10.0.0.1:8080", false)
	found := false
	for _, p := range got {
		if p == "10.0.0.1:8080" {
			found = true
		}
	}
	if !found {
		t.Errorf("auth off: scheme not stripped, got %v", got)
	}
}

func TestParseSpeedFormValidation(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
		want   string
	}{
		{"bad max", map[string]string{"max-speed": "abc"}, "max-speed"},
		{"bad ratio", map[string]string{"max-ratio": "abc"}, "max-ratio"},
		{"negative ratio", map[string]string{"max-ratio": "-1"}, "non-negative"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _ := buildMultipart(t, c.fields)
			_, _, _, err := parseSpeedForm(r, 500)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %q missing %q", err.Error(), c.want)
			}
		})
	}
}
