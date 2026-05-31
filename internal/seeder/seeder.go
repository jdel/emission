// Package seeder is the runtime engine that seeds torrents.
//
// A Manager holds any number of live torrent sessions, each backed by a
// .torrent file under a watched root directory. Each session announces to
// every HTTP tracker of its torrent on the tracker's own schedule and reports
// a simulated upload rate. The CLI watcher and the HTTP API are both thin
// wrappers over Manager.
package seeder

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jdel/emission/internal/client"
	"github.com/jdel/emission/internal/torrent"
	"github.com/jdel/emission/internal/tracker"
	"github.com/rs/zerolog/log"
)

// ErrNotFound is returned by Remove when no torrent has the given ID.
var ErrNotFound = errors.New("torrent not found")

// StatsPoint is one historical sample for a seeded torrent, recorded every
// rateRefresh interval and persisted to a <name>.torrent.stats sidecar file.
type StatsPoint struct {
	TimeMs   int64  `json:"t"` // unix milliseconds
	Rate     uint64 `json:"r"` // simulated upload rate, bytes/sec
	Leechers int64  `json:"l"` // total leechers across all trackers
}

// Status is a point-in-time snapshot of one seeded torrent. JSON tags match
// the shape the web UI expects, so it can be served directly.
type Status struct {
	ID                 string          `json:"id"`       // info hash, hex
	Name               string          `json:"name"`     // torrent display name
	Location           string          `json:"location"` // .torrent path, relative to the watched root
	SizeBytes          uint64          `json:"sizeBytes"`
	UploadedBytes      uint64          `json:"uploadedBytes"`
	RateBytesPerSec    uint64          `json:"rateBytesPerSec"`
	MaxRateBytesPerSec uint64          `json:"maxRateBytesPerSec"` // configured ceiling
	MaxRatio           float64         `json:"maxRatio"`           // upload cap as a multiple of torrent size (0 = unlimited)
	Capped             bool            `json:"capped"`             // true when the ratio cap has been reached
	DeleteOnCap        bool            `json:"deleteOnCap"`        // remove the torrent automatically when capped
	AddedAt            int64           `json:"addedAt"`            // unix milliseconds
	Trackers           []TrackerStatus `json:"trackers"`
}

// TrackerStatus is the latest known state of one tracker of a torrent.
type TrackerStatus struct {
	URL            string `json:"url"`
	Seeders        int    `json:"seeders"`
	Leechers       int    `json:"leechers"`
	IntervalSec    int    `json:"intervalSec"`
	MinIntervalSec int    `json:"minIntervalSec"`
	NextAnnounceAt int64  `json:"nextAnnounceAt"` // unix ms, 0 if unknown
	Status         string `json:"status"`         // "ok" | "failing" | "pending"
}

// StatUpdate carries one new stats data point together with the torrent ID,
// sent to WebSocket clients via SubscribeStats.
type StatUpdate struct {
	ID    string
	Point StatsPoint
}

// Manager runs and tracks torrent seeding sessions. It is safe for concurrent
// use; the watcher and the HTTP API call its methods from many goroutines.
type Manager struct {
	client      *client.Client
	httpClient  *http.Client
	maxRatio    float64 // upload cap as a multiple of torrent size; 0 = unlimited
	autoRemove  bool    // global default: remove torrent on cap (overridden per-torrent by state file)
	torrentsDir string  // absolute path of the watched root

	baseCtx context.Context
	cancel  context.CancelFunc

	mu       sync.Mutex
	sessions map[string]*session // by info-hash ID
	byPath   map[string]*session // by absolute .torrent path
	wg       sync.WaitGroup

	clientMu    sync.Mutex
	userClients map[string]*client.Client // per-owner identity; "" = unowned/root

	proxyDefault string // server-wide --client.proxy; per-user default ("" = direct)
	proxyMu      sync.Mutex
	proxyClients map[string]*http.Client // announce client cached per owner

	proxyStatusMu sync.Mutex
	proxyStatus   map[string]proxyProbe // owner -> last probe result

	settings *settingsStore // per-owner upload bandwidth + seeding profile

	subsMu sync.Mutex
	subs   map[chan struct{}]struct{}

	statSubsMu sync.Mutex
	statSubs   map[chan StatUpdate]struct{}
}

// New creates a Manager. tmpl is the template BitTorrent identity that each
// owner's identity is cloned from; torrentsDir is the watched root that
// .torrent file paths are reported relative to; maxRatio caps the simulated
// upload at that multiple of the torrent size (0 = unlimited); autoRemove
// removes the torrent automatically when the cap is reached; defaultBandwidth
// is the per-user upload ceiling (bytes/sec) applied to users without an
// explicit setting. proxyURL, when set, routes every announce through that one
// proxy (http/https/socks5); empty announces directly. To override the peer
// count requested per announce, set tmpl.NumWant before passing it in.
func New(tmpl *client.Client, torrentsDir string, maxRatio float64, autoRemove bool, defaultBandwidth uint64, proxyURL string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	abs, err := filepath.Abs(torrentsDir)
	if err != nil {
		abs = torrentsDir
	}
	return &Manager{
		client:       tmpl,
		httpClient:   &http.Client{Timeout: 30 * time.Second}, // direct (no proxy)
		maxRatio:     maxRatio,
		autoRemove:   autoRemove,
		torrentsDir:  abs,
		baseCtx:      ctx,
		cancel:       cancel,
		proxyDefault: proxyURL,
		proxyClients: make(map[string]*http.Client),
		proxyStatus:  make(map[string]proxyProbe),
		settings:     loadSettingsStore(filepath.Join(abs, userSettingsFileName), defaultBandwidth),
		sessions:     make(map[string]*session),
		byPath:       make(map[string]*session),
		userClients:  make(map[string]*client.Client),
		subs:         make(map[chan struct{}]struct{}),
		statSubs:     make(map[chan StatUpdate]struct{}),
	}
}

// clientFor returns the BitTorrent identity for owner, cloning the template
// client on first use and reusing it thereafter. Each owner (and the
// unowned/root bucket, "") gets one stable peer_id and key, so a tracker sees a
// consistent identity per user rather than one shared across all of them.
func (m *Manager) clientFor(owner string) (*client.Client, error) {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	if cl, ok := m.userClients[owner]; ok {
		return cl, nil
	}
	cl, err := m.client.Clone()
	if err != nil {
		return nil, err
	}
	m.userClients[owner] = cl
	return cl, nil
}

// cappedRate returns natural unless the sum of owner's natural rates across all
// their torrents exceeds the owner's bandwidth, in which case it scales natural
// down proportionally so the per-user total stays within the ceiling. Each
// torrent's natural rate is recomputed deterministically (the ±20% jitter lives
// in accumulateLoop), so the sum is consistent with every caller's own value.
func (m *Manager) cappedRate(owner string, natural uint64) uint64 {
	budget := m.settings.bandwidth(owner)
	halfSaturation := m.settings.profileHalfSaturation(owner)
	var total uint64
	m.mu.Lock()
	for _, s := range m.sessions {
		if s.owner != owner {
			continue
		}
		var l int64
		for _, ts := range s.trackers {
			l += ts.leechers.Load()
		}
		total += naturalRate(s.maxSpeed.Load(), budget, l, halfSaturation)
	}
	m.mu.Unlock()
	if total == 0 || total <= budget {
		return natural
	}
	// float avoids uint64 overflow on the intermediate product.
	return uint64(float64(natural) * float64(budget) / float64(total))
}

// Bandwidth returns the upload-bandwidth ceiling (bytes/sec) for owner.
func (m *Manager) Bandwidth(owner string) uint64 { return m.settings.bandwidth(owner) }

// DefaultBandwidth returns the ceiling applied to owners without an explicit setting.
func (m *Manager) DefaultBandwidth() uint64 { return m.settings.defBandwidth }

// SetBandwidth sets owner's upload-bandwidth ceiling (bytes/sec) and persists it.
func (m *Manager) SetBandwidth(owner string, bytesPerSec uint64) error {
	return m.settings.setBandwidth(owner, bytesPerSec)
}

// Profile returns the display name for owner's seeding curve
// (stealth/normal/aggressive/custom).
func (m *Manager) Profile(owner string) string { return m.settings.profileName(owner) }

// HalfSaturation returns owner's seeding-curve half-saturation (leechers for
// half speed).
func (m *Manager) HalfSaturation(owner string) float64 {
	return m.settings.profileHalfSaturation(owner)
}

// SetHalfSaturation sets owner's seeding-curve half-saturation and persists it.
func (m *Manager) SetHalfSaturation(owner string, k float64) error {
	return m.settings.setHalfSaturation(owner, k)
}

// proxyProbe is the recorded result of the last reachability test of an owner's
// proxy.
type proxyProbe struct {
	ok  bool
	err string
}

// hostnameRe matches a DNS hostname: dot-separated labels of letters, digits,
// and hyphens (not leading/trailing a label).
var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*$`)

// ValidateProxyURL enforces the strict shape scheme://host:port — an http,
// https, or socks5 scheme, an IP or hostname, and an explicit port, with no
// userinfo, path, query, or fragment. Anything looser could turn the proxy
// field into a data-exfiltration target, so it is rejected. Empty is not valid
// here; callers treat "" as "announce directly" before calling.
func ValidateProxyURL(proxyURL string) error {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("invalid proxy URL: %v", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5":
	default:
		return fmt.Errorf("proxy scheme must be http, https, or socks5 (got %q)", u.Scheme)
	}
	if u.User != nil {
		return errors.New("proxy must not contain credentials; use scheme://host:port")
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return errors.New("proxy must be exactly scheme://host:port (no path or query)")
	}
	host, port := u.Hostname(), u.Port()
	if host == "" || port == "" {
		return errors.New("proxy must include both host and port: scheme://host:port")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return errors.New("proxy port must be a number 1-65535")
	}
	if net.ParseIP(host) == nil && !hostnameRe.MatchString(host) {
		return errors.New("proxy host must be an IP address or hostname")
	}
	return nil
}

// UserProxy returns owner's effective proxy URL — their explicit setting when
// set, otherwise the server default — and whether owner has set one explicitly.
// An effective value of "" means owner announces directly.
func (m *Manager) UserProxy(owner string) (proxyURL string, explicit bool) {
	if p, ok := m.settings.proxy(owner); ok {
		return p, true
	}
	return m.proxyDefault, false
}

// ProxyDefault returns the server-wide default proxy URL (--client.proxy).
func (m *Manager) ProxyDefault() string { return m.proxyDefault }

// effectiveProxy is the proxy URL owner announces through ("" = direct).
func (m *Manager) effectiveProxy(owner string) string {
	px, _ := m.UserProxy(owner)
	return px
}

// trusted reports whether proxyURL is the admin-set CLI default. The default is
// dialed without the internal-address guard; any other (user-supplied) proxy is
// guarded.
func (m *Manager) trusted(proxyURL string) bool {
	return proxyURL != "" && proxyURL == m.proxyDefault
}

// announceClient returns the HTTP client for owner's effective proxy, caching
// one client per owner. A direct ("") proxy uses the shared no-proxy client; a
// user-supplied proxy is dialed through the internal-address guard, the trusted
// CLI default without it. SetUserProxy drops the owner's cached client when the
// proxy changes, so a cached entry always matches the current setting.
func (m *Manager) announceClient(owner string) *http.Client {
	px := m.effectiveProxy(owner)
	if px == "" {
		return m.httpClient
	}
	m.proxyMu.Lock()
	defer m.proxyMu.Unlock()
	if c, ok := m.proxyClients[owner]; ok {
		return c
	}
	u, err := url.Parse(px)
	if err != nil {
		return m.httpClient // already validated on set; fall back to direct
	}
	tr := &http.Transport{Proxy: http.ProxyURL(u)}
	if !m.trusted(px) {
		tr.DialContext = tracker.GuardedDialContext
	}
	c := &http.Client{Timeout: 30 * time.Second, Transport: tr}
	m.proxyClients[owner] = c
	return c
}

// SetUserProxy validates and persists owner's proxy URL. "" means announce
// directly; any non-empty value must satisfy [ValidateProxyURL]. A user may not
// point at a local/private address (unless it is the trusted CLI default); a
// malformed or local value is rejected without persisting.
func (m *Manager) SetUserProxy(owner, proxyURL string) error {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL != "" {
		if err := ValidateProxyURL(proxyURL); err != nil {
			return err
		}
		if !m.trusted(proxyURL) {
			if err := rejectLocalHost(proxyURL); err != nil {
				return err
			}
		}
	}
	if err := m.settings.setProxy(owner, proxyURL); err != nil {
		return err
	}
	// Drop the owner's cached client so the next announce rebuilds it for the
	// new proxy.
	m.proxyMu.Lock()
	if c, ok := m.proxyClients[owner]; ok {
		c.CloseIdleConnections()
		delete(m.proxyClients, owner)
	}
	m.proxyMu.Unlock()
	return nil
}

// rejectLocalHost blocks a proxy whose host is a literal loopback/private/
// link-local address, so a user cannot aim it at an internal service. A
// hostname is allowed through here but still dialed through the guard, so one
// that resolves to an internal address fails when the proxy is used.
func rejectLocalHost(proxyURL string) error {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && tracker.IsDisallowedIP(ip) {
		return errors.New("proxy must be a public address, not a local/private one")
	}
	return nil
}

// proxyProbeURL is the harmless endpoint a probe requests through the proxy to
// confirm it relays traffic; any HTTP response means the proxy works.
const proxyProbeURL = "https://example.com"

// ProbeUserProxy tests owner's effective proxy with a short request and records
// the result for [ProxyStatus]. A direct (empty) proxy is always reachable.
// Returns ok and an error message (empty when ok).
func (m *Manager) ProbeUserProxy(ctx context.Context, owner string) (ok bool, errMsg string) {
	px := m.effectiveProxy(owner)
	if px == "" {
		m.recordProbe(owner, proxyProbe{ok: true})
		return true, ""
	}
	pr := proxyProbe{ok: true}
	if err := probeProxy(ctx, px, !m.trusted(px)); err != nil {
		pr = proxyProbe{ok: false, err: err.Error()}
	}
	m.recordProbe(owner, pr)
	return pr.ok, pr.err
}

func (m *Manager) recordProbe(owner string, pr proxyProbe) {
	m.proxyStatusMu.Lock()
	m.proxyStatus[owner] = pr
	m.proxyStatusMu.Unlock()
}

// ProxyStatus reports owner's proxy state for display: "direct" when the
// effective proxy is empty, "ok"/"error" once probed (with the error message),
// or "unknown" when not probed yet this run.
func (m *Manager) ProxyStatus(owner string) (status, errMsg string) {
	if m.effectiveProxy(owner) == "" {
		return "direct", ""
	}
	m.proxyStatusMu.Lock()
	pr, ok := m.proxyStatus[owner]
	m.proxyStatusMu.Unlock()
	switch {
	case !ok:
		return "unknown", ""
	case pr.ok:
		return "ok", ""
	default:
		return "error", pr.err
	}
}

// probeProxy makes a short HEAD request through proxyURL. Any HTTP response
// counts as success; only a transport failure (refused, timeout, bad proxy)
// returns an error. When guarded, the proxy is dialed through the internal-
// address guard, so one resolving to a local host fails here.
func probeProxy(ctx context.Context, proxyURL string, guarded bool) error {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tr := &http.Transport{Proxy: http.ProxyURL(u), DisableKeepAlives: true} // one-shot; don't pool the conn
	if guarded {
		tr.DialContext = tracker.GuardedDialContext
	}
	c := &http.Client{Transport: tr}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, proxyProbeURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// AddFile reads and parses the .torrent file at path and starts seeding it at a
// leecher-scaled rate up to the owner's bandwidth — or a per-torrent override
// from the state file. The torrent's info hash is its ID; a torrent already
// loaded (same hash) is rejected. path should be absolute.
func (m *Manager) AddFile(path string) (Status, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Status{}, err
	}
	owner := Owner(m.relPath(abs))
	// A state file next to the .torrent overrides the caller's defaults. Without
	// one, a new torrent's max defaults to the owner's current bandwidth.
	maxSpeed := m.settings.bandwidth(owner)
	maxRatio := m.maxRatio
	deleteOnCap := m.autoRemove
	var addedAt time.Time
	var uploadedBytes uint64
	if sMax, sRatio, sAddedAt, sUploaded, sDeleteOnCap, ok := LoadStateFile(abs); ok {
		maxSpeed, maxRatio = sMax, sRatio
		uploadedBytes = sUploaded
		deleteOnCap = sDeleteOnCap
		if sAddedAt > 0 {
			addedAt = time.UnixMilli(sAddedAt)
		}
	}
	if addedAt.IsZero() {
		addedAt = time.Now()
		// Persist so restarts restore the original add time and autoremove default.
		_ = SaveStateFile(abs, maxSpeed, maxRatio, addedAt.UnixMilli(), 0, deleteOnCap)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Status{}, err
	}
	meta, err := torrent.Parse(data)
	if err != nil {
		return Status{}, err
	}
	id := fmt.Sprintf("%x", meta.InfoHash)

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.sessions[id]; dup {
		return Status{}, fmt.Errorf("torrent already loaded: %s", meta.Name)
	}
	cl, err := m.clientFor(owner)
	if err != nil {
		return Status{}, fmt.Errorf("client identity: %w", err)
	}
	s := newSession(m.baseCtx, id, meta, abs, maxSpeed, maxRatio, addedAt, owner, cl, m)
	if uploadedBytes > 0 {
		s.uploaded.Store(uploadedBytes)
	}
	if deleteOnCap {
		s.deleteOnCap.Store(true)
	}
	if pts, err := loadStatsFile(abs + ".stats"); err == nil {
		s.statsBuf = pts
		s.statsFlushed = len(pts) // already on disk
	}
	m.sessions[id] = s
	m.byPath[abs] = s
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		s.run()
	}()
	m.notifyChanged()
	return s.status(), nil
}

// Exists reports whether a torrent with the given info-hash ID is loaded.
func (m *Manager) Exists(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[id]
	return ok
}

// Remove stops seeding the torrent with the given ID (info hash) and deletes
// its backing .torrent file from disk. The session's goroutines send a final
// "stopped" announce as they wind down. Returns an error if no such torrent is
// loaded, or if the file could not be deleted (the session is stopped either
// way).
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		delete(m.byPath, s.path)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	s.cancel()
	m.notifyChanged()
	removeStateFile(s.path)
	removeStatsFile(s.path)
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		log.Error().Err(err).Str("path", s.path).Msg("could not delete torrent file")
		return fmt.Errorf("torrent stopped but file not deleted: %w", err)
	}
	return nil
}

// SetClientOptions updates a live torrent's per-client overrides — max upload
// rate and ratio cap — and persists them to its state file. The change takes
// effect immediately: the current rate is re-rolled against the new ceiling,
// and the upload cap is recomputed against the new ratio (maxRatio == 0 means
// unlimited).
func (m *Manager) SetClientOptions(id string, maxSpeed uint64, maxRatio float64, deleteOnCap bool) error {
	if maxRatio < 0 {
		return fmt.Errorf("max-ratio must be non-negative")
	}
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	s.maxSpeed.Store(maxSpeed)
	s.maxRatio = maxRatio
	s.uploadCap.Store(uploadCapFor(s.meta.Length, maxRatio))
	var leechers int64
	for _, ts := range s.trackers {
		leechers += ts.leechers.Load()
	}
	// Re-roll into the new ceiling immediately; the owner's proportional cap is
	// applied by pickRateLoop on its next tick (cappedRate locks m.mu, which we
	// hold here). naturalRate already clamps to the owner's bandwidth.
	s.rate.Store(naturalRate(maxSpeed, m.settings.bandwidth(s.owner), leechers, m.settings.profileHalfSaturation(s.owner)))
	s.deleteOnCap.Store(deleteOnCap)
	path := s.path
	addedAtMs := s.addedAt.UnixMilli()
	uploadedMs := s.uploaded.Load()
	m.mu.Unlock()
	if err := SaveStateFile(path, maxSpeed, maxRatio, addedAtMs, uploadedMs, deleteOnCap); err != nil {
		return fmt.Errorf("save state file: %w", err)
	}
	return nil
}

// RemoveByPath stops the session backed by the .torrent file at path, without
// touching the file (it is used when the file has already been deleted on
// disk). A path with no matching session is a no-op.
func (m *Manager) RemoveByPath(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	m.mu.Lock()
	s, ok := m.byPath[abs]
	if ok {
		delete(m.byPath, abs)
		delete(m.sessions, s.id)
	}
	m.mu.Unlock()
	if ok {
		s.cancel()
		m.notifyChanged()
	}
}

// RemoveUnder stops every session whose .torrent file lives under dir — used
// when a whole subdirectory is deleted. Files are not touched.
func (m *Manager) RemoveUnder(dir string) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	prefix := abs + string(os.PathSeparator)
	m.mu.Lock()
	var stopped []*session
	for path, s := range m.byPath {
		if strings.HasPrefix(path, prefix) {
			delete(m.byPath, path)
			delete(m.sessions, s.id)
			stopped = append(stopped, s)
		}
	}
	m.mu.Unlock()
	for _, s := range stopped {
		s.cancel()
	}
	if len(stopped) > 0 {
		m.notifyChanged()
	}
}

// relPath returns abs expressed relative to the watched root, for display.
// Falls back to the base name if abs is outside the root.
func (m *Manager) relPath(abs string) string {
	rel, err := filepath.Rel(m.torrentsDir, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Base(abs)
	}
	return rel
}

// List returns a snapshot of every loaded torrent, oldest first.
func (m *Manager) List() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.status())
	}
	// Sort by add time, with ID as a tiebreaker so the order is stable when
	// many torrents are added within the same millisecond (directory scan).
	sort.Slice(out, func(i, j int) bool {
		if out[i].AddedAt != out[j].AddedAt {
			return out[i].AddedAt < out[j].AddedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Owner returns the username a torrent belongs to — the first path segment of
// its location (e.g. "alice/x.torrent" -> "alice"). A torrent stored directly
// in the root has no owner and returns "".
func Owner(location string) string {
	if i := strings.IndexRune(location, filepath.Separator); i >= 0 {
		return location[:i]
	}
	return ""
}

// Visible returns the torrents a viewer may see: their own, plus unowned
// (root-level) torrents which are shared with everyone. An empty viewer
// (authentication disabled) sees every torrent.
func (m *Manager) Visible(viewer string) []Status {
	all := m.List()
	if viewer == "" {
		return all
	}
	out := make([]Status, 0, len(all))
	for _, s := range all {
		if o := Owner(s.Location); o == viewer || o == "" {
			out = append(out, s)
		}
	}
	return out
}

// GetStats returns a copy of the in-memory stats buffer for the torrent with
// the given ID. Returns nil, false when no such torrent is loaded.
func (m *Manager) GetStats(id string) ([]StatsPoint, bool) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return nil, false
	}
	s.statsMu.Lock()
	pts := make([]StatsPoint, len(s.statsBuf))
	copy(pts, s.statsBuf)
	s.statsMu.Unlock()
	return pts, true
}

// Get returns the status of a single torrent by ID.
func (m *Manager) Get(id string) (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return Status{}, false
	}
	return s.status(), true
}

// Page returns a filtered, paginated slice of the torrents visible to viewer
// and the total count after filtering. query is a case-insensitive substring
// matched against the torrent name; an empty query matches all. owner, when
// non-empty, keeps only torrents owned by that user (empty matches all). limit
// <= 0 returns every torrent from offset onward. offset past the end is an
// empty page.
func (m *Manager) Page(offset, limit int, query, viewer, owner string) (items []Status, total int) {
	all := m.Visible(viewer)
	if query != "" {
		q := strings.ToLower(query)
		filtered := make([]Status, 0, len(all))
		for _, s := range all {
			if strings.Contains(strings.ToLower(s.Name), q) {
				filtered = append(filtered, s)
			}
		}
		all = filtered
	}
	if owner != "" {
		filtered := make([]Status, 0, len(all))
		for _, s := range all {
			if Owner(s.Location) == owner {
				filtered = append(filtered, s)
			}
		}
		all = filtered
	}
	total = len(all)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []Status{}, total
	}
	end := total
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return all[offset:end], total
}

// Subscribe returns a channel that receives a signal whenever a torrent is
// added or removed, plus a function to unsubscribe. The channel is buffered
// (depth 1) and coalesces; callers should treat any receive as "list changed,
// refetch". Always call the returned cancel func.
func (m *Manager) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	m.subsMu.Lock()
	m.subs[ch] = struct{}{}
	m.subsMu.Unlock()
	return ch, func() {
		m.subsMu.Lock()
		if _, ok := m.subs[ch]; ok {
			delete(m.subs, ch)
			close(ch)
		}
		m.subsMu.Unlock()
	}
}

// SubscribeStats returns a channel that receives a signal whenever a new stats
// point is recorded for any torrent, plus a function to unsubscribe.
// The channel is buffered (depth 16) and drops on a slow consumer.
func (m *Manager) SubscribeStats() (<-chan StatUpdate, func()) {
	ch := make(chan StatUpdate, 16)
	m.statSubsMu.Lock()
	m.statSubs[ch] = struct{}{}
	m.statSubsMu.Unlock()
	return ch, func() {
		m.statSubsMu.Lock()
		if _, ok := m.statSubs[ch]; ok {
			delete(m.statSubs, ch)
			close(ch)
		}
		m.statSubsMu.Unlock()
	}
}

// notifyStatPoint fans a new stats data point out to all stat subscribers.
func (m *Manager) notifyStatPoint(id string, pt StatsPoint) {
	su := StatUpdate{ID: id, Point: pt}
	m.statSubsMu.Lock()
	for ch := range m.statSubs {
		select {
		case ch <- su:
		default: // slow consumer; drop rather than block
		}
	}
	m.statSubsMu.Unlock()
}

// notifyChanged signals every subscriber that the torrent list changed.
func (m *Manager) notifyChanged() {
	m.subsMu.Lock()
	for ch := range m.subs {
		select {
		case ch <- struct{}{}:
		default: // a pending signal is already queued
		}
	}
	m.subsMu.Unlock()
}

// shutdownGrace bounds how long Shutdown waits for "stopped" announces to
// drain before giving up and letting the process exit.
const shutdownGrace = 6 * time.Second

// Shutdown stops every session and waits — up to shutdownGrace — for the
// "stopped" announces to drain. Stragglers are abandoned so exit stays prompt.
func (m *Manager) Shutdown() {
	m.cancel()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownGrace):
	}
}
