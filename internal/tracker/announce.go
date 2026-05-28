// Package tracker performs HTTP/HTTPS BitTorrent tracker announces.
package tracker

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jdel/emission/internal/bencode"
	"github.com/jdel/emission/internal/client"
	"github.com/jdel/emission/internal/torrent"
)

// Event is the BitTorrent announce event field.
type Event string

const (
	EventNone      Event = ""
	EventStarted   Event = "started"
	EventStopped   Event = "stopped"
	EventCompleted Event = "completed"
)

// Params are the per-announce values substituted into the client query template.
type Params struct {
	Port       uint16
	Uploaded   uint64
	Downloaded uint64
	Left       uint64
	Event      Event
	NumWant    int
	// TrackerID, when non-empty, is echoed back as trackerid= on follow-up
	// announces (the value a tracker sent in an earlier response).
	TrackerID string
	// HTTPClient sends the request. When nil a package-level default is used.
	HTTPClient *http.Client
}

func isDisallowedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

var safeDialer = &net.Dialer{
	Timeout: 10 * time.Second,
	Control: func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host) // address is already resolved here
		if ip == nil || isDisallowedIP(ip) {
			return fmt.Errorf("blocked address: %s", address)
		}
		return nil
	},
}

// defaultClient is the fallback used by Announce when Params.HTTPClient is
// nil. Reusing one Client (one transport) isolates tracker traffic from any
// other HTTP work in the binary and keeps idle tracker connections pooled.
var defaultClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{DialContext: safeDialer.DialContext},
}

// Response is the subset of tracker reply fields this client honors.
type Response struct {
	// Interval the tracker requests between announces.
	Interval time.Duration
	// MinInterval, when present, is the minimum permitted gap.
	MinInterval time.Duration
	Seeders     int
	Leechers    int
	TrackerID   string
	// FailureReason, if non-empty, indicates the tracker refused the announce.
	FailureReason string
	// Warning is an advisory message; announce still succeeded.
	Warning string
}

// Announce sends a single GET to trackerURL using client's identity and
// returns the parsed Response. Network errors and tracker FailureReason are
// both returned as Go errors so callers can back off uniformly.
func Announce(ctx context.Context, trackerURL string, m *torrent.Meta, c *client.Client, p Params) (*Response, error) {
	built := BuildURL(trackerURL, m, c, p)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, built, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	_, headers := c.Query()
	for _, h := range headers {
		req.Header.Set(h.Name, h.Value)
	}

	hc := p.HTTPClient
	if hc == nil {
		hc = defaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("announce: %w", err)
	}
	defer resp.Body.Close()

	var src io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer gz.Close()
		src = gz
	}
	body, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return parseResponse(body)
}

// BuildURL renders the announce URL by substituting all placeholders in the
// client's query template against trackerURL, m, c, and p.
func BuildURL(trackerURL string, m *torrent.Meta, c *client.Client, p Params) string {
	tmpl, _ := c.Query()
	sep := "?"
	if strings.Contains(trackerURL, "?") {
		sep = "&"
	}
	out := trackerURL + sep + tmpl
	out = strings.ReplaceAll(out, "{infohash}", m.InfoHashURLEncoded)
	out = strings.ReplaceAll(out, "{peerid}", c.PeerID)
	out = strings.ReplaceAll(out, "{key}", c.Key)
	out = strings.ReplaceAll(out, "{port}", strconv.FormatUint(uint64(p.Port), 10))
	out = strings.ReplaceAll(out, "{uploaded}", strconv.FormatUint(p.Uploaded, 10))
	out = strings.ReplaceAll(out, "{downloaded}", strconv.FormatUint(p.Downloaded, 10))
	out = strings.ReplaceAll(out, "{left}", strconv.FormatUint(p.Left, 10))
	if p.Event == EventNone {
		// Real clients omit event entirely on regular announces. Drop the
		// whole token rather than leave a bare event=.
		out = strings.ReplaceAll(out, "{event}", "")
		out = strings.ReplaceAll(out, "&event=&", "&")
		out = strings.ReplaceAll(out, "?event=&", "?")
		out = strings.TrimSuffix(out, "&event=")
		out = strings.TrimSuffix(out, "?event=")
	} else {
		out = strings.ReplaceAll(out, "{event}", string(p.Event))
	}
	numwant := p.NumWant
	if numwant == 0 {
		if p.Event == EventStopped {
			numwant = c.NumWantOnStop
		} else {
			numwant = c.NumWant
		}
	}
	out = strings.ReplaceAll(out, "{numwant}", strconv.Itoa(numwant))
	// Strip placeholders we don't support so the URL is still valid.
	for _, ph := range []string{"{ip}", "{ipv6}", "{os}", "{java}", "{locale}"} {
		out = strings.ReplaceAll(out, ph, "")
	}
	// Common pattern in some templates: "ipv6={ipv6}" becomes "ipv6=" — leave
	// it; trackers accept empty values.
	if p.TrackerID != "" {
		out += "&trackerid=" + url.QueryEscape(p.TrackerID)
	}
	return out
}

func parseResponse(body []byte) (*Response, error) {
	v, err := bencode.Decode(body)
	if err != nil {
		return nil, fmt.Errorf("decode tracker response: %w (raw: %q)", err, truncate(body))
	}
	if v.Kind != bencode.KindDict {
		return nil, fmt.Errorf("tracker response is not a dict")
	}
	r := &Response{}
	if f, ok := v.Dict["failure reason"]; ok && f.Kind == bencode.KindBytes {
		r.FailureReason = string(f.Bytes)
		return r, fmt.Errorf("tracker rejected announce: %s", r.FailureReason)
	}
	if w, ok := v.Dict["warning message"]; ok && w.Kind == bencode.KindBytes {
		r.Warning = string(w.Bytes)
	}
	if i, ok := v.Dict["interval"]; ok && i.Kind == bencode.KindInt && i.Int > 0 {
		r.Interval = time.Duration(i.Int) * time.Second
	}
	if i, ok := v.Dict["min interval"]; ok && i.Kind == bencode.KindInt && i.Int > 0 {
		r.MinInterval = time.Duration(i.Int) * time.Second
	}
	if i, ok := v.Dict["complete"]; ok && i.Kind == bencode.KindInt {
		r.Seeders = int(i.Int)
	}
	if i, ok := v.Dict["incomplete"]; ok && i.Kind == bencode.KindInt {
		r.Leechers = int(i.Int)
	}
	if t, ok := v.Dict["tracker id"]; ok && t.Kind == bencode.KindBytes {
		r.TrackerID = string(t.Bytes)
	}
	return r, nil
}

func truncate(b []byte) string {
	if len(b) > 120 {
		return string(b[:120]) + "..."
	}
	return string(b)
}
