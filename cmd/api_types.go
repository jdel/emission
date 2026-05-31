package cmd

import (
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/jdel/emission/internal/seeder"
)

// API request and response shapes. These are the single source of truth for
// both the runtime JSON encoding/decoding and the Swagger schema.

// pagedTorrents is the GET /api/torrents response.
type pagedTorrents struct {
	Items []seeder.Status `json:"items"`
	Total int             `json:"total"`
}

// wsMessage is a server-to-client WebSocket frame.
//   - "stats":         live torrent snapshots, ~1/s
//   - "changed":       torrent list added/removed
//   - "stats_history": full stats buffer sent once on connect, keyed by torrent ID
//   - "stat_point":    one new data point for a single torrent (every rateRefresh)
type wsMessage struct {
	Type     string                         `json:"type"`
	Torrents []seeder.Status                `json:"torrents,omitempty"`
	History  map[string][]seeder.StatsPoint `json:"history,omitempty"`
	ID       string                         `json:"id,omitempty"`
	Point    *seeder.StatsPoint             `json:"point,omitempty"`
}

// uploadResult is the POST /api/torrents response.
type uploadResult struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Notice string `json:"notice,omitempty"` // set when tracker URLs were truncated
}

// speedUpdate is the PATCH /api/torrents/{id} request body.
type speedUpdate struct {
	MaxSpeed    string  `json:"maxSpeed"`
	MaxRatio    float64 `json:"maxRatio"`
	DeleteOnCap bool    `json:"deleteOnCap"`
}

// bandwidthInfo is the GET /api/bandwidth response (the caller's own settings).
type bandwidthInfo struct {
	Bandwidth      uint64  `json:"bandwidth"`      // bytes/sec
	Default        uint64  `json:"default"`        // server default, bytes/sec
	Profile        string  `json:"profile"`        // display name: stealth|normal|aggressive|custom
	HalfSaturation float64 `json:"halfSaturation"` // leechers for half speed
}

// bandwidthUpdate is the PUT /api/bandwidth and admin set-bandwidth request
// body. Optional fields left nil are unchanged.
type bandwidthUpdate struct {
	Bandwidth      string   `json:"bandwidth"`                // e.g. "2M"
	HalfSaturation *float64 `json:"halfSaturation,omitempty"` // leechers for half speed
}

// proxyInfo is the GET/PUT /api/proxy response: the caller's effective proxy,
// the server default they inherit, and the last reachability probe result.
type proxyInfo struct {
	Proxy   string `json:"proxy"`           // effective proxy URL ("" = announce directly)
	Default string `json:"default"`         // server default (--client.proxy), "" if none
	Status  string `json:"status"`          // "ok" | "error" | "direct" | "unknown"
	Error   string `json:"error,omitempty"` // probe error when status is "error"
}

// proxyUpdate is the PUT /api/proxy request body. An empty proxy means announce
// directly.
type proxyUpdate struct {
	Proxy string `json:"proxy"`
}

// errorResponse is the body shape every non-2xx handler returns.
type errorResponse struct {
	Error string `json:"error"`
}

// authStatusResponse is the GET /api/auth/status response. Auth-disabled
// servers return {authenticated: true} with no username; auth-enabled
// servers return {authenticated: true, username: "..."} for a valid
// session, or {authenticated: false} otherwise. The client derives whether
// auth is configured from these two fields.
type authStatusResponse struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
}

// inviteBody is the POST /api/auth/register/begin request body.
type inviteBody struct {
	Invite string `json:"invite"`
}

// inviteRequest is the POST /api/auth/invite request body.
type inviteRequest struct {
	Username string `json:"username"`
}

// inviteResponse is the POST /api/auth/invite response.
type inviteResponse struct {
	URL  string `json:"url"`
	Code string `json:"code"`
}

// deviceInfo is one entry in the GET /api/auth/users response array.
type deviceInfo struct {
	ID             string  `json:"id"` // base64url credential id
	Username       string  `json:"username"`
	InvitedBy      string  `json:"invitedBy,omitempty"` // empty for the bootstrap admin
	AddedAt        int64   `json:"addedAt"`
	Bandwidth      uint64  `json:"bandwidth"`      // this user's upload ceiling, bytes/sec
	Profile        string  `json:"profile"`        // this user's seeding-curve display name
	HalfSaturation float64 `json:"halfSaturation"` // leechers for half speed
}

// registerChallenge is the POST /api/auth/register/begin response.
type registerChallenge struct {
	CeremonyID string                       `json:"ceremonyId"`
	Options    *protocol.CredentialCreation `json:"options"`
	Username   string                       `json:"username"`
}

// loginChallenge is the POST /api/auth/login/begin response.
type loginChallenge struct {
	CeremonyID string                        `json:"ceremonyId"`
	Options    *protocol.CredentialAssertion `json:"options"`
}

// authResult is the success body for register/finish and login/finish. The
// cookie carries the actual session; this body just confirms it.
type authResult struct {
	Authenticated bool `json:"authenticated"`
}

// pendingInvite is one entry in the GET /api/auth/invites response array.
type pendingInvite struct {
	Token     string `json:"token"`
	Username  string `json:"username"`
	CreatedBy string `json:"createdBy,omitempty"`
	ExpiresAt int64  `json:"expiresAt"` // unix ms
}
