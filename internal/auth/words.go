package auth

import (
	"crypto/rand"
	_ "embed"
	"math/big"
	"strings"
)

// wordlistRaw is the EFF Large Wordlist (7776 words, CC-BY) — the same list
// Bitwarden uses for passphrases. Embedded one word per line.
//
//go:embed words.txt
var wordlistRaw string

// words is the parsed wordlist.
var words = strings.Fields(wordlistRaw)

// randomWords returns n crypto-random words from the EFF wordlist joined with
// hyphens, e.g. "arrogant-jimmy-dumpster". Three words is ~39 bits — ample for
// a single-use, 24-hour invite token, and easy to read aloud.
func randomWords(n int) (string, error) {
	parts := make([]string, n)
	max := big.NewInt(int64(len(words)))
	for i := range parts {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		parts[i] = words[idx.Int64()]
	}
	return strings.Join(parts, "-"), nil
}
