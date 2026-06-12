// Package model holds the persisted data shapes shared between the
// application logic and the storage layer. Types here carry their JSON tags so
// every storage backend round-trips the same on-disk (or in-DB) encoding.
package model

import "github.com/go-webauthn/webauthn/webauthn"

// TorrentState is the per-torrent override persisted alongside a seeded
// torrent. Speeds are raw bytes/sec so the round-trip is lossless; the
// human-readable display lives in the UI. MaxRatio is a multiple of torrent
// size; 0 means unlimited. AddedAt is unix milliseconds; 0 means unknown
// (state predating the field).
type TorrentState struct {
	MaxSpeed      uint64  `json:"maxSpeed"`
	MaxRatio      float64 `json:"maxRatio,omitempty"`
	AddedAt       int64   `json:"addedAt,omitempty"`
	UploadedBytes uint64  `json:"uploadedBytes,omitempty"`
	DeleteOnCap   bool    `json:"deleteOnCap,omitempty"`
}

// StatsPoint is one historical sample for a seeded torrent.
type StatsPoint struct {
	TimeMs   int64  `json:"t"` // unix milliseconds
	Rate     uint64 `json:"r"` // simulated upload rate, bytes/sec
	Leechers int64  `json:"l"` // total leechers across all trackers
}

// UserSettings is one owner's persisted seeding preferences.
type UserSettings struct {
	Bandwidth      uint64  `json:"bandwidth,omitempty"`      // 0 = use the store default
	HalfSaturation float64 `json:"halfSaturation,omitempty"` // 0 = normal; leechers for half speed
	Profile        string  `json:"profile,omitempty"`        // legacy; migrated to HalfSaturation on load
	Proxy          *string `json:"proxy,omitempty"`          // nil = use server default; set (incl "") = explicit ("" = direct)
}

// StoredCredential is one registered passkey plus bookkeeping. Username is a
// cosmetic label chosen at registration — every credential still belongs to
// the single WebAuthn user; there is no per-username access control.
type StoredCredential struct {
	Username   string              `json:"username"`
	InvitedBy  string              `json:"invitedBy,omitempty"` // empty for the bootstrap admin
	AddedAt    int64               `json:"addedAt"`             // unix milliseconds
	Credential webauthn.Credential `json:"credential"`
}

// CredentialSet is the persisted passkey store: the single WebAuthn user
// handle and every registered credential.
type CredentialSet struct {
	UserID      []byte             `json:"userId"`
	Credentials []StoredCredential `json:"credentials"`
}
