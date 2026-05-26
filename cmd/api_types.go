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
	ID   string `json:"id"`
	Name string `json:"name"`
}

// speedUpdate is the PATCH /api/torrents/{id} request body.
type speedUpdate struct {
	MinSpeed    string  `json:"minSpeed"`
	MaxSpeed    string  `json:"maxSpeed"`
	MaxRatio    float64 `json:"maxRatio"`
	DeleteOnCap bool    `json:"deleteOnCap"`
}

// errorResponse is the body shape every non-2xx handler returns.
type errorResponse struct {
	Error string `json:"error"`
}

// authStatusResponse is the GET /api/auth/status response.
type authStatusResponse struct {
	AuthEnabled        bool   `json:"authEnabled"`
	Authenticated      bool   `json:"authenticated"`
	Username           string `json:"username,omitempty"`
	DeviceCount        int    `json:"deviceCount,omitempty"`
	BootstrapAvailable bool   `json:"bootstrapAvailable,omitempty"`
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
	ID        string `json:"id"` // base64url credential id
	Username  string `json:"username"`
	InvitedBy string `json:"invitedBy,omitempty"` // empty for the bootstrap admin
	AddedAt   int64  `json:"addedAt"`
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
