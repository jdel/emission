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

// Status is a point-in-time snapshot of one seeded torrent. JSON tags match
// the shape the web UI expects, so it can be served directly.
type Status struct {
	ID                 string          `json:"id"`       // info hash, hex
	Name               string          `json:"name"`     // torrent display name
	Location           string          `json:"location"` // .torrent path, relative to the watched root
	SizeBytes          uint64          `json:"sizeBytes"`
	UploadedBytes      uint64          `json:"uploadedBytes"`
	RateBytesPerSec    uint64          `json:"rateBytesPerSec"`
	MinRateBytesPerSec uint64          `json:"minRateBytesPerSec"` // configured floor
	MaxRateBytesPerSec uint64          `json:"maxRateBytesPerSec"` // configured ceiling
	MaxRatio           float64         `json:"maxRatio"`           // upload cap as a multiple of torrent size (0 = unlimited)
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

// Manager runs and tracks torrent seeding sessions. It is safe for concurrent
// use; the watcher and the HTTP API call its methods from many goroutines.
type Manager struct {
	client      *client.Client
	httpClient  *http.Client
	maxRatio    float64 // upload cap as a multiple of torrent size; 0 = unlimited
	torrentsDir string  // absolute path of the watched root

	baseCtx context.Context
	cancel  context.CancelFunc

	mu       sync.Mutex
	sessions map[string]*session // by info-hash ID
	byPath   map[string]*session // by absolute .torrent path
	wg       sync.WaitGroup

	subsMu sync.Mutex
	subs   map[chan struct{}]struct{}
}

// New creates a Manager. client is the BitTorrent identity used for every
// torrent; torrentsDir is the watched root that .torrent file paths are
// reported relative to; maxRatio caps the simulated upload at that multiple
// of the torrent size (0 = unlimited). To override the peer count requested
// per announce, set client.NumWant before passing it in.
func New(client *client.Client, torrentsDir string, maxRatio float64) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	abs, err := filepath.Abs(torrentsDir)
	if err != nil {
		abs = torrentsDir
	}
	return &Manager{
		client:      client,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		maxRatio:    maxRatio,
		torrentsDir: abs,
		baseCtx:     ctx,
		cancel:      cancel,
		sessions:    make(map[string]*session),
		byPath:      make(map[string]*session),
		subs:        make(map[chan struct{}]struct{}),
	}
}

// AddFile reads and parses the .torrent file at path and starts seeding it at a
// rate that varies randomly between minSpeed and maxSpeed (bytes/sec). The
// torrent's info hash is its ID; a torrent already loaded (same hash) is
// rejected. path should be absolute.
func (m *Manager) AddFile(path string, minSpeed, maxSpeed uint64) (Status, error) {
	if minSpeed > maxSpeed {
		return Status{}, fmt.Errorf("min-speed exceeds max-speed")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Status{}, err
	}
	// A sidecar JSON next to the .torrent overrides the caller's defaults.
	// Written at upload time or by SetClientOptions (live edits).
	maxRatio := m.maxRatio
	if sMin, sMax, sRatio, ok := LoadSidecar(abs); ok {
		minSpeed, maxSpeed, maxRatio = sMin, sMax, sRatio
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
	s := newSession(m.baseCtx, id, meta, abs, minSpeed, maxSpeed, maxRatio, m)
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
	removeSidecar(s.path)
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		log.Error().Err(err).Str("path", s.path).Msg("could not delete torrent file")
		return fmt.Errorf("torrent stopped but file not deleted: %w", err)
	}
	return nil
}

// SetClientOptions updates a live torrent's per-client overrides — min/max
// upload rate and ratio cap — and persists them to its sidecar JSON. The new
// range takes effect immediately: the current rate is re-rolled into the new
// band, and the upload cap is recomputed against the new ratio (maxRatio == 0
// means unlimited).
func (m *Manager) SetClientOptions(id string, minSpeed, maxSpeed uint64, maxRatio float64) error {
	if minSpeed > maxSpeed {
		return fmt.Errorf("min-speed exceeds max-speed")
	}
	if maxRatio < 0 {
		return fmt.Errorf("max-ratio must be non-negative")
	}
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	s.minSpeed.Store(minSpeed)
	s.maxSpeed.Store(maxSpeed)
	s.maxRatio = maxRatio
	s.uploadCap.Store(uploadCapFor(s.meta.Length, maxRatio))
	s.rate.Store(pickRate(minSpeed, maxSpeed))
	path := s.path
	m.mu.Unlock()
	if err := SaveSidecar(path, minSpeed, maxSpeed, maxRatio); err != nil {
		return fmt.Errorf("save sidecar: %w", err)
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
