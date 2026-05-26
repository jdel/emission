package client

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"unicode/utf8"
)

// BitTorrent protocol fixes peer_id at 20 bytes. Tracker keys are 8 chars by
// convention (every shipped profile uses 8).
const (
	peerIDLength = 20
	keyLength    = 8
)

// hashKey returns a random 8-char hex string. If noLeadingZero is true, the
// first nibble is non-zero. keyCase selects letter case:
//   - "lower" → a-f
//   - "upper", "none", or ""  → A-F (default)
//
// Used for HASH and HASH_NO_LEADING_ZERO algorithms.
func hashKey(noLeadingZero bool, keyCase string) string {
	const hex = "0123456789ABCDEF"
	lower := keyCase == "lower"
	var b strings.Builder
	b.Grow(keyLength)
	for b.Len() < keyLength {
		i := rand.IntN(16)
		if i == 0 && noLeadingZero && b.Len() == 0 {
			continue
		}
		c := hex[i]
		if lower && c >= 'A' && c <= 'F' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// digitRangeToHex picks a random integer in [lo, hi] and formats it as hex
// without leading zeros. keyCase controls letter case ("upper" → A-F,
// anything else → a-f). Used by Transmission for the tracker key.
func digitRangeToHex(lo, hi uint32, keyCase string) string {
	if hi < lo {
		hi = lo
	}
	v := lo + uint32(rand.Uint64N(uint64(hi-lo)+1))
	s := fmt.Sprintf("%x", v)
	if keyCase == "upper" {
		s = strings.ToUpper(s)
	}
	return s
}

// prefixedRandomPool builds a peer_id by concatenating prefix with random
// runes drawn from pool until the total reaches peerIDLength bytes. Used by
// Transmission (prefix "-TR3000-", pool "0-9a-z").
//
// If a candidate rune would overshoot peerIDLength as UTF-8 it is skipped
// (with a single-char fallback drawn from pool) rather than truncated, so
// the output is always valid UTF-8.
func prefixedRandomPool(prefix, pool string) string {
	if prefix == "" || pool == "" {
		return ""
	}
	if len(prefix) >= peerIDLength {
		return prefix[:peerIDLength]
	}
	poolRunes := []rune(pool)
	var b strings.Builder
	b.Grow(peerIDLength)
	b.WriteString(prefix)
	for b.Len() < peerIDLength {
		r := poolRunes[rand.IntN(len(poolRunes))]
		size := utf8.RuneLen(r)
		if size < 0 {
			continue // invalid rune in pool; skip
		}
		if b.Len()+size > peerIDLength {
			// This rune would overflow. Find an ASCII rune in the pool that
			// fits exactly; if none, stop early (defensive — shipped pools
			// always contain enough ASCII to top up).
			fit := pickFittingRune(poolRunes, peerIDLength-b.Len())
			if fit < 0 {
				break
			}
			b.WriteRune(fit)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// pickFittingRune returns a rune from pool whose UTF-8 encoding is exactly
// maxBytes long, or -1 if none qualifies.
func pickFittingRune(pool []rune, maxBytes int) rune {
	for _, r := range pool {
		if utf8.RuneLen(r) == maxBytes {
			return r
		}
	}
	return -1
}
