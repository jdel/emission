package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jdel/emission/internal/seeder"
)

// handleWS streams live updates to a client: a "stats" snapshot once per
// second, and a "changed" frame whenever the torrent list is added to or
// removed from. The list itself (paging, search) stays on the REST endpoint.
//
//	@Summary	WebSocket live stats feed
//	@Tags		realtime
//	@Description	Upgrades to WebSocket. Pushes {type:"stats",torrents:[...]} ~1/s and {type:"changed"} on list changes.
//	@Router		/api/ws [get]
func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
	// Origin restriction: when --http.public-url is set we know the canonical
	// host browsers will connect from, so we lock the WebSocket to that host
	// (plus localhost for dev). When unset, the deployment is assumed
	// local-network-only, so we keep the wildcard.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.wsOriginPatterns,
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	// The viewer is fixed for the life of the connection; stats are scoped to
	// the torrents this user may see (everything, for auth-off or the admin).
	viewer := s.viewer(r)

	// CloseRead drains incoming frames and returns a context cancelled when the
	// client disconnects — we never expect client-to-server messages.
	ctx := c.CloseRead(r.Context())

	changed, unsubscribe := s.mgr.Subscribe()
	defer unsubscribe()
	statUpdates, unsubStats := s.mgr.SubscribeStats()
	defer unsubStats()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Send an immediate live snapshot.
	if wsjson.Write(ctx, c, wsMessage{Type: "stats", Torrents: s.mgr.Visible(viewer)}) != nil {
		return
	}
	// Send full stats history for all visible torrents.
	if visible := s.mgr.Visible(viewer); len(visible) > 0 {
		history := make(map[string][]seeder.StatsPoint, len(visible))
		for _, t := range visible {
			if pts, ok := s.mgr.GetStats(t.ID); ok && len(pts) > 0 {
				history[t.ID] = pts
			}
		}
		if len(history) > 0 {
			if wsjson.Write(ctx, c, wsMessage{Type: "stats_history", History: history}) != nil {
				return
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if wsjson.Write(ctx, c, wsMessage{Type: "stats", Torrents: s.mgr.VisibleSnapshot(viewer)}) != nil {
				return
			}
		case _, ok := <-changed:
			if !ok {
				return
			}
			if wsjson.Write(ctx, c, wsMessage{Type: "changed"}) != nil {
				return
			}
		case su, ok := <-statUpdates:
			if !ok {
				return
			}
			// Visibility check: skip if this torrent isn't visible to the viewer.
			if st, exists := s.mgr.Get(su.ID); exists {
				owner := seeder.Owner(st.Location)
				if viewer != "" && owner != viewer && owner != "" {
					continue
				}
			}
			if wsjson.Write(ctx, c, wsMessage{Type: "stat_point", ID: su.ID, Point: &su.Point}) != nil {
				return
			}
		}
	}
}

// wsOrigins builds the accepted Origin patterns for /api/ws.
//
// Auth ON:  publicURL is the canonical base URL — parse it, take its host.
// Auth OFF: publicURL is a comma-separated list of raw Origin host patterns
// (path.Match globs, e.g. "10.0.0.*:8080") for LAN use. A bare "*" is
// dropped to prevent blanket cross-origin access.
// In both cases, loopback is always accepted; empty publicURL fails safe to
// loopback only.
func wsOrigins(publicURL string, authEnabled bool) []string {
	patterns := []string{"localhost:*", "127.0.0.1:*", "[::1]:*"}
	if publicURL == "" {
		return patterns
	}
	if authEnabled {
		if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
			patterns = append(patterns, u.Host)
		}
		return patterns
	}
	// Auth off: raw comma-separated origin host patterns (globs).
	for _, p := range strings.Split(publicURL, ",") {
		p = strings.TrimSpace(p)
		if i := strings.Index(p, "://"); i >= 0 {
			p = p[i+3:] // strip scheme if pasted as a full URL
		}
		if p == "" || p == "*" {
			continue
		}
		patterns = append(patterns, p)
	}
	return patterns
}
