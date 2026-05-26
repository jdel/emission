// Package units converts between byte counts and human-readable strings.
package units

import (
	"fmt"
	"strconv"
	"strings"
)

// FormatBytes renders a byte count with a binary-scaled unit, e.g. "1.4 GB".
// It is the rough inverse of ParseRate (minus the unit).
func FormatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ParseRate parses strings like "500K", "2M", "1.5G", "1000" into bytes/sec.
// Suffixes are binary (K=1024). A trailing "B" and a trailing "/s" are both
// accepted and ignored. Bare digits are bytes/sec.
//
// Returns an error for negative, non-numeric, or unknown-suffix inputs.
func ParseRate(s string) (uint64, error) {
	orig := s
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "/s")
	s = strings.TrimSuffix(s, "/S")
	if len(s) == 0 {
		return 0, fmt.Errorf("empty rate")
	}
	// Strip trailing 'B' or 'b'.
	if c := s[len(s)-1]; c == 'B' || c == 'b' {
		s = s[:len(s)-1]
	}
	mult := uint64(1)
	if len(s) > 0 {
		switch s[len(s)-1] {
		case 'K', 'k':
			mult = 1 << 10
			s = s[:len(s)-1]
		case 'M', 'm':
			mult = 1 << 20
			s = s[:len(s)-1]
		case 'G', 'g':
			mult = 1 << 30
			s = s[:len(s)-1]
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("parse rate %q: %w", orig, err)
	}
	if v < 0 {
		return 0, fmt.Errorf("negative rate %q", orig)
	}
	return uint64(v * float64(mult)), nil
}
