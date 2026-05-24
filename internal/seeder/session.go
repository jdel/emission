package seeder

import (
	"context"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jdel/emission/internal/torrent"
	"github.com/jdel/emission/internal/tracker"
	"github.com/jdel/emission/internal/units"
	"github.com/rs/zerolog/log"
)

// tracker state constants, stored in trackerState.state.
const (
	statePending int32 = iota
	stateOK
	stateFailing
)

// rateRefresh is how often a session picks a new simulated upload rate.
const rateRefresh = 30 * time.Second

// advertisedPort is the listening port reported to trackers. We never actually
// accept peer connections, so the value is cosmetic — 6881 is the canonical
// BitTorrent port and plausible for any client.
const advertisedPort = 6881

// stopAnnounceTimeout caps the final "stopped" announce sent at shutdown — it
// is a courtesy, so a slow or dead tracker must not delay exit.
const stopAnnounceTimeout = 5 * time.Second

// session is one torrent being seeded: a shared upload counter, a rate that
// drifts over time, and one announce loop per tracker URL.
type session struct {
	// --- Immutable after construction ---
	id       string
	meta     *torrent.Meta
	path     string // absolute path of the backing .torrent file
	addedAt  time.Time
	mgr      *Manager
	ctx      context.Context
	cancel   context.CancelFunc
	trackers []*trackerState

	// --- Guarded by Manager.mu ---
	// Read in status() (called under m.mu); written in SetClientOptions.
	maxRatio float64

	// --- Atomic: read by goroutines, written by SetClientOptions ---
	minSpeed  atomic.Uint64
	maxSpeed  atomic.Uint64
	uploaded  atomic.Uint64 // bytes uploaded this session
	rate      atomic.Uint64 // current simulated rate, bytes/sec
	uploadCap atomic.Uint64 // max bytes to upload (0 = unlimited)
}

// trackerState holds one tracker's latest values. Written by that tracker's
// announce goroutine, read by status(); every field is an atomic.
type trackerState struct {
	url            string
	seeders        atomic.Int64
	leechers       atomic.Int64
	intervalSec    atomic.Int64
	minIntervalSec atomic.Int64
	nextUnixMs     atomic.Int64
	state          atomic.Int32
}

func newSession(parent context.Context, id string, meta *torrent.Meta, path string, minSpeed, maxSpeed uint64, maxRatio float64, m *Manager) *session {
	ctx, cancel := context.WithCancel(parent)
	s := &session{
		id:       id,
		meta:     meta,
		path:     path,
		addedAt:  time.Now(),
		mgr:      m,
		ctx:      ctx,
		cancel:   cancel,
		maxRatio: maxRatio,
	}
	s.minSpeed.Store(minSpeed)
	s.maxSpeed.Store(maxSpeed)
	s.uploadCap.Store(uploadCapFor(meta.Length, maxRatio))
	for _, u := range meta.AnnounceURLs {
		s.trackers = append(s.trackers, &trackerState{url: u})
	}
	s.rate.Store(pickRate(minSpeed, maxSpeed))
	return s
}

// uploadCapFor returns the absolute byte cap implied by sizeBytes × ratio,
// or 0 (unlimited) when either factor is zero/negative.
func uploadCapFor(sizeBytes uint64, ratio float64) uint64 {
	if ratio <= 0 || sizeBytes == 0 {
		return 0
	}
	return uint64(float64(sizeBytes) * ratio)
}

// run starts the rate picker, the upload accumulator, and one announce loop per
// tracker, then blocks until they all finish (i.e. until ctx is cancelled).
func (s *session) run() {
	log.Info().Str("name", s.meta.Name).Uint64("size", s.meta.Length).Int("trackers", len(s.meta.AnnounceURLs)).Msg("seeding")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.pickRateLoop() }()
	go func() { defer wg.Done(); s.accumulateLoop() }()
	for _, ts := range s.trackers {
		wg.Add(1)
		go func(ts *trackerState) {
			defer wg.Done()
			s.runTracker(ts)
		}(ts)
	}
	wg.Wait()
	log.Info().Str("name", s.meta.Name).Str("uploaded", units.FormatBytes(s.uploaded.Load())).Msg("stopped")
}

// pickRateLoop refreshes the simulated upload rate every rateRefresh.
func (s *session) pickRateLoop() {
	t := time.NewTicker(rateRefresh)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.rate.Store(pickRate(s.minSpeed.Load(), s.maxSpeed.Load()))
		}
	}
}

// accumulateLoop advances the shared upload counter once per second by the
// current rate. Two safety guards suppress accumulation:
//   - no leechers anywhere in the swarm (nobody to upload to)
//   - uploaded already reached the ratio cap
func (s *session) accumulateLoop() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			var leechers int64
			for _, ts := range s.trackers {
				leechers += ts.leechers.Load()
			}
			if leechers == 0 {
				continue
			}
			up := s.uploaded.Load()
			if cap := s.uploadCap.Load(); cap > 0 && up >= cap {
				continue
			}
			rate := s.rate.Load()
			// Cap at MaxInt64 to keep the signed arithmetic below safe. Real
			// rates are orders of magnitude smaller than this; the cap is a
			// belt-and-braces guard, not an expected branch.
			if rate > math.MaxInt64 {
				rate = math.MaxInt64
			}
			// One signed draw in [-span, +span], span = ~20% of rate.
			span := int64(rate/5 + 1)
			delta := rand.Int64N(2*span+1) - span
			n := int64(rate) + delta
			if n < 1 {
				n = 1 // never accumulate zero/negative
			}
			s.uploaded.Add(uint64(n))
		}
	}
}

// runTracker drives the announce loop for one tracker URL. It never sleeps less
// than the tracker's reported "min interval", even after an error backoff.
func (s *session) runTracker(ts *trackerState) {
	params := func(ev tracker.Event) tracker.Params {
		return tracker.Params{
			Port:       advertisedPort,
			Uploaded:   s.uploaded.Load(),
			Downloaded: s.meta.Length,
			Event:      ev,
			HTTPClient: s.mgr.httpClient,
		}
	}

	var knownMin time.Duration
	var interval time.Duration

	// Skip EventStarted if the session was cancelled before we ever ran —
	// no point sending a "started" that we immediately follow with "stopped".
	if s.ctx.Err() != nil {
		return
	}
	resp, err := tracker.Announce(s.ctx, ts.url, s.meta, s.mgr.client, params(tracker.EventStarted))
	knownMin = maxDur(knownMin, minInterval(resp))
	ts.apply(resp, err)
	interval = clampMin(pickInterval(resp, err, 30*time.Minute), knownMin)
	ts.setNext(interval)
	logAnnounce(s.meta.Name, ts.url, tracker.EventStarted, resp, err, s.uploaded.Load(), interval)

	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			stopCtx, cancel := context.WithTimeout(context.Background(), stopAnnounceTimeout)
			resp, err := tracker.Announce(stopCtx, ts.url, s.meta, s.mgr.client, params(tracker.EventStopped))
			cancel()
			logAnnounce(s.meta.Name, ts.url, tracker.EventStopped, resp, err, s.uploaded.Load(), 0)
			return
		case <-timer.C:
		}
		resp, err := tracker.Announce(s.ctx, ts.url, s.meta, s.mgr.client, params(tracker.EventNone))
		knownMin = maxDur(knownMin, minInterval(resp))
		ts.apply(resp, err)
		next := pickInterval(resp, err, interval)
		if err != nil {
			next = backoff(interval)
		}
		next = clampMin(next, knownMin)
		ts.setNext(next)
		logAnnounce(s.meta.Name, ts.url, tracker.EventNone, resp, err, s.uploaded.Load(), next)
		interval = next
		timer.Reset(next)
	}
}

// status builds a snapshot of this session for the API and CLI.
func (s *session) status() Status {
	trk := make([]TrackerStatus, len(s.trackers))
	for i, ts := range s.trackers {
		trk[i] = TrackerStatus{
			URL:            ts.url,
			Seeders:        int(ts.seeders.Load()),
			Leechers:       int(ts.leechers.Load()),
			IntervalSec:    int(ts.intervalSec.Load()),
			MinIntervalSec: int(ts.minIntervalSec.Load()),
			NextAnnounceAt: ts.nextUnixMs.Load(),
			Status:         stateName(ts.state.Load()),
		}
	}
	return Status{
		ID:                 s.id,
		Name:               s.meta.Name,
		Location:           s.mgr.relPath(s.path),
		SizeBytes:          s.meta.Length,
		UploadedBytes:      s.uploaded.Load(),
		RateBytesPerSec:    s.rate.Load(),
		MinRateBytesPerSec: s.minSpeed.Load(),
		MaxRateBytesPerSec: s.maxSpeed.Load(),
		MaxRatio:           s.maxRatio,
		AddedAt:            s.addedAt.UnixMilli(),
		Trackers:           trk,
	}
}

// apply records the result of an announce into the tracker state.
func (ts *trackerState) apply(resp *tracker.Response, err error) {
	if err != nil || resp == nil {
		ts.state.Store(stateFailing)
		return
	}
	ts.state.Store(stateOK)
	ts.seeders.Store(int64(resp.Seeders))
	ts.leechers.Store(int64(resp.Leechers))
	ts.intervalSec.Store(int64(resp.Interval / time.Second))
	ts.minIntervalSec.Store(int64(resp.MinInterval / time.Second))
}

// setNext records when this tracker will next announce.
func (ts *trackerState) setNext(d time.Duration) {
	ts.nextUnixMs.Store(time.Now().Add(d).UnixMilli())
}

func stateName(v int32) string {
	switch v {
	case stateOK:
		return "ok"
	case stateFailing:
		return "failing"
	default:
		return "pending"
	}
}

func logAnnounce(name, url string, ev tracker.Event, resp *tracker.Response, err error, uploaded uint64, next time.Duration) {
	if err != nil {
		log.Error().Err(err).Str("torrent", name).Str("tracker", url).Str("event", string(ev)).Msg("announce failed")
		return
	}
	e := log.Debug().Str("torrent", name).Str("tracker", url).Str("event", string(ev)).
		Int("seeders", resp.Seeders).Int("leechers", resp.Leechers).
		Str("uploaded", units.FormatBytes(uploaded)).
		Str("interval", resp.Interval.String()).
		Str("next", next.String())
	if resp.MinInterval > 0 {
		e = e.Str("min_interval", resp.MinInterval.String())
	}
	e.Msg("announce ok")
}

// --- small helpers ----------------------------------------------------------

func pickRate(min, max uint64) uint64 {
	if max <= min {
		return min
	}
	return min + rand.Uint64N(max-min+1)
}

func minInterval(resp *tracker.Response) time.Duration {
	if resp == nil {
		return 0
	}
	return resp.MinInterval
}

func maxDur(a, b time.Duration) time.Duration {
	if b > a {
		return b
	}
	return a
}

func clampMin(d, floor time.Duration) time.Duration {
	if floor > 0 && d < floor {
		return floor
	}
	return d
}

func pickInterval(resp *tracker.Response, err error, fallback time.Duration) time.Duration {
	if err != nil || resp == nil {
		return fallback
	}
	iv := resp.Interval
	if resp.MinInterval > iv {
		iv = resp.MinInterval
	}
	if iv <= 0 {
		return fallback
	}
	return iv
}

func backoff(d time.Duration) time.Duration {
	d *= 2
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	if d < 30*time.Second {
		d = 30 * time.Second
	}
	return d
}
