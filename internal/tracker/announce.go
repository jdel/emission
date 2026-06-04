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

// IsDisallowedIP reports whether ip is one we refuse to connect to: loopback,
// private, link-local, or unspecified. Exported so other packages can apply the
// same policy (e.g. user-configured proxies).
func IsDisallowedIP(ip net.IP) bool {
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
		if ip == nil || IsDisallowedIP(ip) {
			return fmt.Errorf("blocked address: %s", address)
		}
		return nil
	},
}

// GuardedDialContext dials only public addresses, refusing loopback, private,
// link-local, and unspecified ones (checked after DNS resolution, so it also
// stops a hostname that resolves to an internal address). Reused by callers
// that must not be pointed at internal hosts, such as user-configured proxies.
var GuardedDialContext = safeDialer.DialContext

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

// BuildURL renders the announce URL from the client's query template: each
// "key={placeholder}" gets its value substituted, and any param whose value
// resolves empty (an unsupported or absent placeholder, e.g. {ipv6} or {event}
// on a regular announce) is dropped rather than left as a dangling "key=".
//
// The query is assembled by hand rather than with net/url.Values, on purpose:
// Values.Encode sorts params alphabetically and percent-encodes their values —
// both wrong here. The per-client parameter order is itself a fingerprint we
// must preserve, and info_hash/peer_id/key are already raw-byte %-encoded, so
// re-encoding would double-escape them.
func BuildURL(trackerURL string, m *torrent.Meta, c *client.Client, p Params) string {
	vals := announceValues(m, c, p)
	tmpl, _ := c.Query()

	var b strings.Builder
	b.WriteString(trackerURL)
	b.WriteByte(querySep(trackerURL))

	first := true
	for _, param := range strings.Split(tmpl, "&") {
		key, valTmpl, ok := strings.Cut(param, "=")
		if !ok {
			continue // not a key=value fragment
		}
		val := valTmpl
		if filled, isPlaceholder := vals[valTmpl]; isPlaceholder {
			val = filled
		}
		if val == "" {
			continue // unfilled placeholder → omit the whole param
		}
		writeParam(&b, &first, key, val)
	}
	if p.TrackerID != "" {
		writeParam(&b, &first, "trackerid", url.QueryEscape(p.TrackerID))
	}
	return b.String()
}

// announceValues maps each template placeholder to its filled value. An empty
// value means "drop this param": unsupported placeholders ({ip}, {ipv6}, {os},
// {java}, {locale}) and {event} on a regular announce.
func announceValues(m *torrent.Meta, c *client.Client, p Params) map[string]string {
	return map[string]string{
		"{infohash}":   m.InfoHashURLEncoded,
		"{peerid}":     c.PeerID,
		"{key}":        c.Key,
		"{port}":       strconv.FormatUint(uint64(p.Port), 10),
		"{uploaded}":   strconv.FormatUint(p.Uploaded, 10),
		"{downloaded}": strconv.FormatUint(p.Downloaded, 10),
		"{left}":       strconv.FormatUint(p.Left, 10),
		"{numwant}":    strconv.Itoa(numWant(p, c)),
		"{event}":      string(p.Event), // "" on a regular announce
		"{ip}":         "",
		"{ipv6}":       "",
		"{os}":         "",
		"{java}":       "",
		"{locale}":     "",
	}
}

// numWant resolves the peer count to request: an explicit Params.NumWant wins,
// else the client's stop/normal default for the event.
func numWant(p Params, c *client.Client) int {
	if p.NumWant != 0 {
		return p.NumWant
	}
	if p.Event == EventStopped {
		return c.NumWantOnStop
	}
	return c.NumWant
}

// querySep is the character joining trackerURL and the query: '&' when the URL
// already carries a query, '?' otherwise.
func querySep(trackerURL string) byte {
	if strings.Contains(trackerURL, "?") {
		return '&'
	}
	return '?'
}

// writeParam appends key=val, prefixing '&' for every param after the first.
func writeParam(b *strings.Builder, first *bool, key, val string) {
	if *first {
		*first = false
	} else {
		b.WriteByte('&')
	}
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(val)
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
