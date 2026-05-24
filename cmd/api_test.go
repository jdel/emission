package cmd

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeTargetPath(t *testing.T) {
	dir := t.TempDir()
	s := &server{torrentsDir: dir}

	cases := []struct {
		name        string
		username    string
		filename    string
		wantErr     bool
		wantSuffix  string // checked when wantErr is false; relative to dir
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
	min, max, ratio, override, err := parseSpeedForm(r, 100, 500)
	if err != nil {
		t.Fatal(err)
	}
	if min != 100 || max != 500 || ratio != 0 {
		t.Errorf("got %d/%d ratio=%v", min, max, ratio)
	}
	if override {
		t.Error("override should be false when no fields supplied")
	}
}

func TestParseSpeedFormOverride(t *testing.T) {
	r, _ := buildMultipart(t, map[string]string{
		"min-speed": "200K",
		"max-speed": "1M",
		"max-ratio": "2.5",
	})
	min, max, ratio, override, err := parseSpeedForm(r, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if min != 200*1024 || max != 1<<20 {
		t.Errorf("speeds = %d/%d", min, max)
	}
	if ratio != 2.5 {
		t.Errorf("ratio = %v", ratio)
	}
	if !override {
		t.Error("override should be true")
	}
}

func TestWsOrigins(t *testing.T) {
	// No public URL → wildcard (local-network mode).
	if got := wsOrigins(""); len(got) != 1 || got[0] != "*" {
		t.Errorf("empty publicURL: got %v", got)
	}
	// Public URL → restricted to that host + localhost variants.
	got := wsOrigins("https://emission.example.com:8443")
	want := []string{"localhost:*", "127.0.0.1:*", "[::1]:*", "emission.example.com:8443"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}
	// Malformed publicURL: still returns localhost patterns, drops the bad host.
	got = wsOrigins("://bad")
	for _, p := range got {
		if strings.Contains(p, "bad") {
			t.Errorf("malformed URL leaked into patterns: %v", got)
		}
	}
}

func TestParseSpeedFormValidation(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]string
		want   string
	}{
		{"bad min", map[string]string{"min-speed": "abc"}, "min-speed"},
		{"bad max", map[string]string{"max-speed": "abc"}, "max-speed"},
		{"bad ratio", map[string]string{"max-ratio": "abc"}, "max-ratio"},
		{"negative ratio", map[string]string{"max-ratio": "-1"}, "non-negative"},
		{"min exceeds max", map[string]string{"min-speed": "1M", "max-speed": "100K"}, "exceeds"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _ := buildMultipart(t, c.fields)
			_, _, _, _, err := parseSpeedForm(r, 100, 500)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %q missing %q", err.Error(), c.want)
			}
		})
	}
}
