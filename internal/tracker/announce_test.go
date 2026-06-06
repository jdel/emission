package tracker

import (
	"compress/gzip"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jdel/emission/internal/client"
	"github.com/jdel/emission/internal/torrent"
)

func TestBuildURLSubstitutes(t *testing.T) {
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	m := &torrent.Meta{
		InfoHashURLEncoded: "%aa%bb",
	}
	got := BuildURL("http://t.example/a", m, c, Params{
		Port: 6881, Uploaded: 1024, Downloaded: 0, Left: 0,
		Event: EventStarted, NumWant: 50,
	})
	want := []string{
		"info_hash=%aa%bb",
		"peer_id=" + c.PeerID,
		"key=" + c.Key,
		"port=6881",
		"uploaded=1024",
		"downloaded=0",
		"left=0",
		"event=started",
		"numwant=50",
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in %s", w, got)
		}
	}
	if strings.Contains(got, "{") {
		t.Errorf("unresolved placeholder in %s", got)
	}
}

func TestBuildURLNumWantFallback(t *testing.T) {
	// transmission-4.0.6: numwant=80, numwantOnStop=0.
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	m := &torrent.Meta{InfoHashURLEncoded: "%aa%bb"}

	cases := []struct {
		name string
		p    Params
		want string
	}{
		{"regular uses NumWant", Params{Event: EventNone}, "numwant=80"},
		{"started uses NumWant", Params{Event: EventStarted}, "numwant=80"},
		{"stop uses NumWantOnStop", Params{Event: EventStopped}, "numwant=0"},
		{"explicit overrides on stop", Params{Event: EventStopped, NumWant: 50}, "numwant=50"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildURL("http://t.example/a", m, c, tc.p)
			if !strings.Contains(got, tc.want) {
				t.Errorf("missing %q in %s", tc.want, got)
			}
		})
	}
}

func TestBuildURLOmitsEmptyEvent(t *testing.T) {
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	m := &torrent.Meta{InfoHashURLEncoded: "%aa%bb"}

	// Regular announce: event must be absent entirely, not a bare event=.
	none := BuildURL("http://t.example/a", m, c, Params{Event: EventNone})
	if strings.Contains(none, "event=") {
		t.Errorf("EventNone left an event= in %s", none)
	}
	if strings.Contains(none, "&&") || strings.HasSuffix(none, "&") {
		t.Errorf("EventNone produced a malformed query: %s", none)
	}

	// Non-empty events must survive intact, with the key left untouched
	// (guards the strip-on-substring regression).
	started := BuildURL("http://t.example/a", m, c, Params{Event: EventStarted})
	if !strings.Contains(started, "event=started") {
		t.Errorf("missing event=started in %s", started)
	}
	if !strings.Contains(started, "key="+c.Key) {
		t.Errorf("key corrupted by event handling in %s", started)
	}
}

// queryKeys returns the parameter keys of url's query, in order.
func queryKeys(u string) []string {
	q := u
	if i := strings.IndexByte(u, '?'); i >= 0 {
		q = u[i+1:]
	}
	var keys []string
	for _, p := range strings.Split(q, "&") {
		if p == "" {
			continue
		}
		k, _, _ := strings.Cut(p, "=")
		keys = append(keys, k)
	}
	return keys
}

// TestBuildURLPreservesParamOrder pins the per-client parameter order — it is a
// fingerprint, so the query must follow the template's order exactly (e.g. never
// alphabetized, as net/url.Values.Encode would). Empty placeholders are dropped
// in place without disturbing the order of the rest; trackerid trails.
func TestBuildURLPreservesParamOrder(t *testing.T) {
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	m := &torrent.Meta{InfoHashURLEncoded: "%aa%bb"}

	// transmission-4.0.6 template order:
	//   info_hash peer_id port uploaded downloaded left numwant key
	//   compact supportcrypto event ipv6
	// On a started announce, {ipv6} is unsupported → dropped; event stays.
	got := queryKeys(BuildURL("http://t.example/a", m, c,
		Params{Event: EventStarted, TrackerID: "tid"}))
	want := []string{
		"info_hash", "peer_id", "port", "uploaded", "downloaded", "left",
		"numwant", "key", "compact", "supportcrypto", "event", "trackerid",
	}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("param order broken at %d: got %v, want %v", i, got, want)
		}
	}

	// A regular announce drops both {event} and {ipv6}, order otherwise intact.
	none := queryKeys(BuildURL("http://t.example/a", m, c, Params{Event: EventNone}))
	wantNone := []string{
		"info_hash", "peer_id", "port", "uploaded", "downloaded", "left",
		"numwant", "key", "compact", "supportcrypto",
	}
	if len(none) != len(wantNone) {
		t.Fatalf("regular keys = %v, want %v", none, wantNone)
	}
	for i := range wantNone {
		if none[i] != wantNone[i] {
			t.Fatalf("regular param order broken at %d: got %v, want %v", i, none, wantNone)
		}
	}
}

func TestBuildURLTrackerID(t *testing.T) {
	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	m := &torrent.Meta{InfoHashURLEncoded: "%aa%bb"}

	// Absent when empty.
	if got := BuildURL("http://t.example/a", m, c, Params{Event: EventNone}); strings.Contains(got, "trackerid=") {
		t.Errorf("unexpected trackerid in %s", got)
	}
	// Appended and URL-escaped when present.
	got := BuildURL("http://t.example/a", m, c, Params{Event: EventNone, TrackerID: "abc 1/2"})
	if !strings.Contains(got, "trackerid=abc+1%2F2") {
		t.Errorf("missing/unescaped trackerid in %s", got)
	}
}

func TestParseTrackerResponse(t *testing.T) {
	// Real-ish response: complete=5, incomplete=2, interval=1800
	body := []byte("d8:completei5e10:incompletei2e8:intervali1800ee")
	r, err := parseResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if r.Interval != 1800*time.Second {
		t.Errorf("interval = %s", r.Interval)
	}
	if r.Seeders != 5 || r.Leechers != 2 {
		t.Errorf("seeders=%d leechers=%d", r.Seeders, r.Leechers)
	}
}

func TestParseTrackerFailure(t *testing.T) {
	body := []byte("d14:failure reason8:bad infoe")
	_, err := parseResponse(body)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bad info") {
		t.Errorf("got %v", err)
	}
}

func TestParseTrackerWarningAndID(t *testing.T) {
	body := []byte("d15:warning message9:obsolete!10:tracker id6:abc123e")
	r, err := parseResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if r.Warning != "obsolete!" {
		t.Errorf("warning = %q", r.Warning)
	}
	if r.TrackerID != "abc123" {
		t.Errorf("tracker id = %q", r.TrackerID)
	}
}

func TestParseTrackerNotDict(t *testing.T) {
	if _, err := parseResponse([]byte("i42e")); err == nil {
		t.Fatal("expected error for non-dict body")
	}
}

func TestParseTrackerInvalidBencode(t *testing.T) {
	_, err := parseResponse([]byte("garbage"))
	if err == nil {
		t.Fatal("expected error for invalid bencode")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v", err)
	}
}

// TestAnnounceCapsResponseBody guards continuous-improvement finding #2: a
// malicious tracker returning an endless body must not be buffered whole. The
// server streams until the client hangs up; with the read capped, Announce stops
// at the limit and returns; without it, io.ReadAll never finishes and Announce
// hangs (caught by the timeout).
func TestAnnounceCapsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		buf := make([]byte, 32<<10)
		for {
			if _, err := w.Write(buf); err != nil { // client closed after the cap
				return
			}
		}
	}))
	defer srv.Close()

	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		_, _ = Announce(context.Background(), srv.URL, newTestMeta(), c, Params{HTTPClient: srv.Client()})
		close(done)
	}()
	select {
	case <-done: // returned at the cap — good
	case <-time.After(3 * time.Second):
		t.Fatal("Announce did not return on an endless response — read is not capped")
	}
}

func newTestMeta() *torrent.Meta {
	return &torrent.Meta{InfoHashURLEncoded: "%aa%bb"}
}

func TestAnnounceHappy(t *testing.T) {
	body := []byte("d8:completei5e10:incompletei2e8:intervali1800ee")
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Announce(context.Background(), srv.URL, newTestMeta(), c, Params{
		HTTPClient: srv.Client(),
		Port:       6881,
		Uploaded:   1024,
		NumWant:    50,
		Event:      EventStarted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Seeders != 5 || resp.Leechers != 2 {
		t.Errorf("seeders=%d leechers=%d", resp.Seeders, resp.Leechers)
	}
	if resp.Interval != 1800*time.Second {
		t.Errorf("interval = %s", resp.Interval)
	}
	if !strings.Contains(gotPath, "info_hash=%aa%bb") {
		t.Errorf("server saw path %q (info_hash missing)", gotPath)
	}
}

func TestAnnounceGzipResponse(t *testing.T) {
	body := []byte("d8:completei3e10:incompletei7e8:intervali900ee")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		_, _ = gz.Write(body)
	}))
	defer srv.Close()

	c, err := client.New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Announce(context.Background(), srv.URL, newTestMeta(), c, Params{
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Seeders != 3 || resp.Leechers != 7 {
		t.Errorf("decoded gzip wrong: seeders=%d leechers=%d", resp.Seeders, resp.Leechers)
	}
}

func TestAnnounceTrackerFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("d14:failure reason13:torrent bannede"))
	}))
	defer srv.Close()

	c, _ := client.New("transmission-4.0.6")
	_, err := Announce(context.Background(), srv.URL, newTestMeta(), c, Params{HTTPClient: srv.Client()})
	if err == nil || !strings.Contains(err.Error(), "torrent banned") {
		t.Fatalf("err = %v", err)
	}
}

func TestAnnounceNetworkError(t *testing.T) {
	c, _ := client.New("transmission-4.0.6")
	// Pointing at a closed port — connect refused.
	_, err := Announce(context.Background(), "http://127.0.0.1:1/", newTestMeta(), c, Params{})
	if err == nil {
		t.Fatal("expected network error")
	}
}

func TestIsDisallowedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",
		"::1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.0.1",
		"0.0.0.0",
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test IP %q", s)
		}
		if !IsDisallowedIP(ip) {
			t.Errorf("expected %s to be disallowed", s)
		}
	}

	public := []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"}
	for _, s := range public {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test IP %q", s)
		}
		if IsDisallowedIP(ip) {
			t.Errorf("expected %s to be allowed", s)
		}
	}
}

func TestDefaultClientBlocksPrivateAddresses(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1:80/",
		"http://10.0.0.1:80/",
		"http://192.168.1.1:80/",
		"http://169.254.169.254/",
	}
	c, _ := client.New("transmission-4.0.6")
	for _, u := range blocked {
		_, err := Announce(context.Background(), u, newTestMeta(), c, Params{})
		if err == nil {
			t.Errorf("expected blocked error for %s, got nil", u)
		}
		if !strings.Contains(err.Error(), "blocked address") {
			t.Errorf("unexpected error for %s: %v", u, err)
		}
	}
}
