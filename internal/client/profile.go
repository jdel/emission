package client

// Profile is the JSON-decoded shape of a clients/*.json file. It fully
// describes how to impersonate one BitTorrent client version: how to generate
// the peer_id and key, how to URL-encode them, the announce query template,
// and the HTTP headers to send.
type Profile struct {
	KeyGenerator    KeyGenSpec     `json:"keyGenerator"`
	PeerIDGenerator PeerGenSpec    `json:"peerIdGenerator"`
	URLEncoder      URLEncoderSpec `json:"urlEncoder"`
	// Query is the URL query template with {placeholder} tokens. See
	// [Client.Query] for the list of recognized placeholders.
	Query string `json:"query"`
	// NumWant is the default peer count requested on a normal announce.
	NumWant int `json:"numwant"`
	// NumWantOnStop is the peer count requested on event=stopped (usually 0).
	NumWantOnStop int `json:"numwantOnStop"`
	// RequestHeaders are the HTTP headers the tracker expects from this client.
	RequestHeaders []Header `json:"requestHeaders"`
}

// KeyGenSpec configures how the tracker key is produced and refreshed.
type KeyGenSpec struct {
	Algorithm AlgoSpec `json:"algorithm"`
	// RefreshOn is one of NEVER, TIMED_OR_AFTER_STARTED_ANNOUNCE,
	// TORRENT_VOLATILE, TORRENT_PERSISTENT. This package does not act on it;
	// it is exposed so callers can implement their own refresh policy.
	RefreshOn string `json:"refreshOn"`
	// RefreshEvery is the interval (in announces) for TIMED refresh, or 0.
	RefreshEvery int `json:"refreshEvery,omitempty"`
	// KeyCase is "upper", "lower", or "none" (used for HASH / DIGIT_RANGE).
	KeyCase string `json:"keyCase"`
}

// PeerGenSpec configures how the peer_id is produced and refreshed.
type PeerGenSpec struct {
	Algorithm AlgoSpec `json:"algorithm"`
	RefreshOn string   `json:"refreshOn"`
	// ShouldURLEncode is set in some profiles but not consulted by this port —
	// the peer_id is always URL-encoded since it contains arbitrary bytes.
	ShouldURLEncode bool `json:"shouldUrlEncode"`
}

// AlgoSpec is the union of fields used by all generator algorithms. Only the
// fields relevant to AlgoSpec.Type are populated for a given profile.
//
// Recognized Type values:
//   - HASH                                              — 8-char hex, possibly cased
//   - HASH_NO_LEADING_ZERO                              — same, first nibble != 0
//   - DIGIT_RANGE_TRANSFORMED_TO_HEX_WITHOUT_LEADING_ZEROES — int in [lo,hi] formatted hex
//   - REGEX                                             — string matching Pattern
//   - RANDOM_POOL_WITH_CHECKSUM                         — Prefix + random chars from CharactersPool
type AlgoSpec struct {
	Type                string `json:"type"`
	Length              int    `json:"length,omitempty"`
	Pattern             string `json:"pattern,omitempty"`
	Prefix              string `json:"prefix,omitempty"`
	CharactersPool      string `json:"charactersPool,omitempty"`
	Base                int    `json:"base,omitempty"`
	InclusiveLowerBound uint32 `json:"inclusiveLowerBound,omitempty"`
	InclusiveUpperBound uint32 `json:"inclusiveUpperBound,omitempty"`
}

// URLEncoderSpec configures URL-encoding for the peer_id and key.
//
// EncodingExclusionPattern is parsed for compatibility with the profile JSON
// schema but not honoured: encoding always uses the form-urlencoded
// unreserved set.
type URLEncoderSpec struct {
	EncodingExclusionPattern string `json:"encodingExclusionPattern"`
	// EncodedHexCase is "upper" or "lower" — controls case of %HH bytes.
	EncodedHexCase string `json:"encodedHexCase"`
}

// Header is one HTTP header sent on every tracker announce.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
