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
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jdel/emission/internal/client"
	"github.com/jdel/emission/internal/torrent"
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
// explicit setting. To override the peer count requested per announce, set
// tmpl.NumWant before passing it in.
func New(tmpl *client.Client, torrentsDir string, maxRatio float64, autoRemove bool, defaultBandwidth uint64) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	abs, err := filepath.Abs(torrentsDir)
	if err != nil {
		abs = torrentsDir
	}
	return &Manager{
		client:      tmpl,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		maxRatio:    maxRatio,
		autoRemove:  autoRemove,
		torrentsDir: abs,
		baseCtx:     ctx,
		cancel:      cancel,
		settings:    loadSettingsStore(filepath.Join(abs, userSettingsFileName), defaultBandwidth),
		sessions:    make(map[string]*session),
		byPath:      make(map[string]*session),
		userClients: make(map[string]*client.Client),
		subs:        make(map[chan struct{}]struct{}),
		statSubs:    make(map[chan StatUpdate]struct{}),
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
	k := m.settings.profileK(owner)
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
		total += naturalRate(s.maxSpeed.Load(), budget, l, k)
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

// Profile returns owner's seeding profile (stealth/normal/aggressive).
func (m *Manager) Profile(owner string) string { return m.settings.profileName(owner) }

// SetProfile sets owner's seeding profile and persists it.
func (m *Manager) SetProfile(owner, name string) error {
	return m.settings.setProfile(owner, name)
}

// AddFile reads and parses the .torrent file at path and starts seeding it at a
// leecher-scaled rate up to maxSpeed (bytes/sec), bounded by the owner's
// bandwidth. The torrent's info hash is its ID; a torrent already loaded (same
// hash) is rejected. path should be absolute.
func (m *Manager) AddFile(path string, maxSpeed uint64) (Status, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Status{}, err
	}
	// A state file next to the .torrent overrides the caller's defaults.
	// Written at upload time or by SetClientOptions (live edits).
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
	owner := Owner(m.relPath(abs))
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
	s.rate.Store(naturalRate(maxSpeed, m.settings.bandwidth(s.owner), leechers, m.settings.profileK(s.owner)))
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
// matched against the torrent name; an empty query matches all. limit <= 0
// returns every torrent from offset onward. offset past the end is an empty
// page.
func (m *Manager) Page(offset, limit int, query, viewer string) (items []Status, total int) {
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
