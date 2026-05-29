// Package torrent parses .torrent metainfo files.
package torrent

import (
	"crypto/sha1"
	"fmt"
	"net/url"
	"strings"

	"github.com/jdel/emission/internal/bencode"
)

// Meta is the subset of .torrent metadata needed to seed.
type Meta struct {
	Name string
	// Length is the total size of the torrent payload in bytes.
	Length uint64
	// Private indicates the torrent flagged private=1 (no DHT, no PEX).
	Private bool
	// AnnounceURLs is the deduplicated union of "announce" and "announce-list".
	AnnounceURLs []string
	// InfoHash is SHA-1 over the raw bytes of the info dictionary as they
	// appear in the source file. Match this exactly or the tracker will
	// reject the announce.
	InfoHash [20]byte
	// InfoHashURLEncoded is InfoHash percent-encoded for use in the
	// {infohash} query placeholder.
	InfoHashURLEncoded string
	// TruncatedTrackers is the number of tracker URLs dropped because the
	// announce list exceeded maxAnnounceURLs. Zero means nothing was dropped.
	TruncatedTrackers int
}

// Parse decodes a bencoded .torrent and extracts metadata.
func Parse(data []byte) (*Meta, error) {
	root, err := bencode.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("decode bencode: %w", err)
	}
	if root.Kind != bencode.KindDict {
		return nil, fmt.Errorf("torrent root is not a dict")
	}

	info, ok := root.Dict["info"]
	if !ok || info.Kind != bencode.KindDict {
		return nil, fmt.Errorf("missing or invalid info dict")
	}
	hash := sha1.Sum(data[info.Start:info.End])

	name, err := dictString(info, "name")
	if err != nil {
		return nil, err
	}

	length, err := totalLength(info)
	if err != nil {
		return nil, err
	}

	var private bool
	if v, ok := info.Dict["private"]; ok && v.Kind == bencode.KindInt && v.Int == 1 {
		private = true
	}

	urls, found := announceURLs(root)
	if len(urls) == 0 {
		return nil, fmt.Errorf("no announce URLs in torrent")
	}

	return &Meta{
		Name:               name,
		Length:             length,
		Private:            private,
		AnnounceURLs:       urls,
		InfoHash:           hash,
		InfoHashURLEncoded: urlEncodeBytes(hash[:]),
		TruncatedTrackers:  found - len(urls),
	}, nil
}

func dictString(v bencode.Value, key string) (string, error) {
	x, ok := v.Dict[key]
	if !ok || x.Kind != bencode.KindBytes {
		return "", fmt.Errorf("missing string field %q", key)
	}
	return string(x.Bytes), nil
}

// totalLength returns the payload size in bytes — either info.length (single
// file mode) or sum of info.files[].length (multi-file mode).
func totalLength(info bencode.Value) (uint64, error) {
	if l, ok := info.Dict["length"]; ok && l.Kind == bencode.KindInt {
		if l.Int < 0 {
			return 0, fmt.Errorf("negative length")
		}
		return uint64(l.Int), nil
	}
	files, ok := info.Dict["files"]
	if !ok || files.Kind != bencode.KindList {
		return 0, fmt.Errorf("info has neither length nor files")
	}
	var total uint64
	for i, f := range files.List {
		if f.Kind != bencode.KindDict {
			return 0, fmt.Errorf("files[%d] not a dict", i)
		}
		l, ok := f.Dict["length"]
		if !ok || l.Kind != bencode.KindInt || l.Int < 0 {
			return 0, fmt.Errorf("files[%d].length invalid", i)
		}
		total += uint64(l.Int)
	}
	return total, nil
}

// maxAnnounceURLs is the maximum number of tracker URLs kept per torrent.
// Private trackers use one announce URL (occasionally 2-3 redundant variants);
// anything beyond this is a sign of abuse or an unusable public torrent.
const maxAnnounceURLs = 5

// announceURLs gathers the union of "announce" and all URLs nested in
// "announce-list" (a list of tiers, each a list of URL strings).
// Only http(s) URLs are kept; UDP and others are skipped for this build.
// Returns the kept URLs and the total number of distinct supported URLs found
// before truncation, so callers can surface a notice when extras were dropped.
func announceURLs(root bencode.Value) (kept []string, found int) {
	seen := map[string]bool{}
	add := func(s string) {
		if !isSupportedHTTP(s) || seen[s] {
			return
		}
		seen[s] = true
		found++
		if len(kept) < maxAnnounceURLs {
			kept = append(kept, s)
		}
	}
	if al, ok := root.Dict["announce-list"]; ok && al.Kind == bencode.KindList {
		for _, tier := range al.List {
			if tier.Kind != bencode.KindList {
				continue
			}
			for _, u := range tier.List {
				if u.Kind == bencode.KindBytes {
					add(string(u.Bytes))
				}
			}
		}
	}
	if a, ok := root.Dict["announce"]; ok && a.Kind == bencode.KindBytes {
		add(string(a.Bytes))
	}
	return kept, found
}

// isSupportedHTTP returns true for http(s) URLs that aren't on the .local
// pseudo-TLD. UDP trackers are intentionally skipped (not yet implemented).
func isSupportedHTTP(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if strings.HasSuffix(strings.ToLower(host), ".local") {
		return false
	}
	return true
}

// urlEncodeBytes percent-encodes every byte except form-urlencoded unreserved
// (A-Za-z0-9*-._). Used for the info_hash query parameter so the tracker
// sees the exact 20 raw bytes.
func urlEncodeBytes(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, len(b)*3)
	for _, c := range b {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '*' || c == '-' || c == '.' || c == '_' {
			out = append(out, c)
			continue
		}
		out = append(out, '%', hex[c>>4], hex[c&0x0f])
	}
	return string(out)
}
