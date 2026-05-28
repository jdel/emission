// Package client builds BitTorrent client identities (peer_id, key, query
// template, HTTP headers) used to impersonate real clients on tracker
// announces. Profiles for 85+ client versions ship embedded as JSON files
// under clients/. List names with [Versions]; build one with [New].
//
// Profiles declare refreshOn policies for peer_id and key (NEVER,
// TIMED_OR_AFTER_STARTED_ANNOUNCE, TORRENT_VOLATILE, TORRENT_PERSISTENT).
// The package does not enforce them; callers decide when to invoke
// [Client.GeneratePeerID] and [Client.GenerateKey] from their own announce
// scheduler. Refresh metadata is exposed via [Client.Profile].
package client

import "fmt"

// Client is a torrent client identity used for HTTP tracker announces.
// It carries a generated peer_id and key, the URL query template, and the
// HTTP headers to send. Build one with [New]; refresh credentials with
// [Client.GeneratePeerID] and [Client.GenerateKey].
//
// All fields are safe to read after construction. The type is not safe for
// concurrent use: callers refreshing credentials must serialize access.
type Client struct {
	// Version is the profile name passed to New (e.g. "transmission-4.0.6").
	Version string
	// Profile is the raw decoded profile. Read it for refresh metadata.
	Profile *Profile
	// PeerID is the URL-encoded peer_id, ready to drop into the {peerid}
	// placeholder of the query template.
	PeerID string
	// Key is the URL-encoded tracker key, ready for the {key} placeholder.
	Key string
	// NumWant is the peer count to request on normal announces (from profile).
	NumWant int
	// NumWantOnStop is the peer count to request on event=stopped.
	NumWantOnStop int
}

// New builds a Client for the named profile version (e.g. "transmission-4.0.6").
// It loads the embedded profile, generates an initial peer_id and key, and
// returns the ready-to-use Client. Use [Versions] to enumerate valid names.
//
// Returns an error if version is unknown or the profile specifies an
// unsupported algorithm.
func New(version string) (*Client, error) {
	p, err := loadProfile(version)
	if err != nil {
		return nil, err
	}
	c := &Client{
		Version:       version,
		Profile:       p,
		NumWant:       p.NumWant,
		NumWantOnStop: p.NumWantOnStop,
	}
	if err := c.GeneratePeerID(); err != nil {
		return nil, fmt.Errorf("generate peer id: %w", err)
	}
	if err := c.GenerateKey(); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	return c, nil
}

// Clone returns a new Client with the same profile and peer-count settings but
// a freshly generated peer_id and key, giving each logical peer (e.g. a
// distinct user) its own stable identity. The clones are independent.
func (c *Client) Clone() (*Client, error) {
	n := &Client{
		Version:       c.Version,
		Profile:       c.Profile,
		NumWant:       c.NumWant,
		NumWantOnStop: c.NumWantOnStop,
	}
	if err := n.GeneratePeerID(); err != nil {
		return nil, fmt.Errorf("clone peer id: %w", err)
	}
	if err := n.GenerateKey(); err != nil {
		return nil, fmt.Errorf("clone key: %w", err)
	}
	return n, nil
}

// GeneratePeerID generates a fresh peer_id, URL-encodes it, and stores it
// in c.PeerID. Call this when the profile's peer refresh policy says it's
// time (e.g. TORRENT_VOLATILE → new ID per torrent session).
func (c *Client) GeneratePeerID() error {
	algo := c.Profile.PeerIDGenerator.Algorithm
	upperHex := c.Profile.URLEncoder.EncodedHexCase == "upper"
	var raw []byte
	switch algo.Type {
	case "REGEX":
		b, err := genFromPattern(algo.Pattern)
		if err != nil {
			return err
		}
		raw = b
	case "RANDOM_POOL_WITH_CHECKSUM":
		raw = []byte(prefixedRandomPool(algo.Prefix, algo.CharactersPool))
	default:
		return fmt.Errorf("unsupported peer id algorithm %q", algo.Type)
	}
	if len(raw) > peerIDLength {
		raw = raw[:peerIDLength]
	}
	c.PeerID = urlEncodeBytes(raw, upperHex)
	return nil
}

// GenerateKey generates a fresh tracker key, URL-encodes it, and stores it
// in c.Key. Call per the profile's KeyGenerator.RefreshOn policy.
func (c *Client) GenerateKey() error {
	algo := c.Profile.KeyGenerator.Algorithm
	keyCase := c.Profile.KeyGenerator.KeyCase
	upperHex := c.Profile.URLEncoder.EncodedHexCase == "upper"
	var raw []byte
	switch algo.Type {
	case "HASH":
		raw = []byte(hashKey(false, keyCase))
	case "HASH_NO_LEADING_ZERO":
		raw = []byte(hashKey(true, keyCase))
	case "DIGIT_RANGE_TRANSFORMED_TO_HEX_WITHOUT_LEADING_ZEROES":
		raw = []byte(digitRangeToHex(algo.InclusiveLowerBound, algo.InclusiveUpperBound, keyCase))
	case "REGEX":
		b, err := genFromPattern(algo.Pattern)
		if err != nil {
			return err
		}
		if len(b) > keyLength {
			b = b[:keyLength]
		}
		raw = b
	default:
		return fmt.Errorf("unsupported key algorithm %q", algo.Type)
	}
	c.Key = urlEncodeBytes(raw, upperHex)
	return nil
}

// Query returns the announce URL query template (with {placeholders} still in
// place) and the HTTP headers to attach to the tracker request.
//
// The caller must substitute every placeholder present in the template before
// sending the request. Recognized placeholders:
//
//   - {infohash}    info hash, URL-encoded
//   - {peerid}      use c.PeerID directly (already URL-encoded)
//   - {key}         use c.Key directly (already URL-encoded)
//   - {port}        listening port, decimal
//   - {uploaded}    bytes uploaded, decimal
//   - {downloaded}  bytes downloaded, decimal
//   - {left}        bytes remaining to download, decimal
//   - {event}       "started", "stopped", "completed", or empty
//   - {numwant}     desired peer count (use c.NumWant or c.NumWantOnStop)
//
// Profile-specific placeholders (only emitted by some clients):
//
//   - {ip}, {ipv6}  local IPs
//   - {os}, {java}  Vuze user-agent fields
//   - {locale}      µTorrent Accept-Language
func (c *Client) Query() (template string, headers []Header) {
	return c.Profile.Query, c.Profile.RequestHeaders
}
