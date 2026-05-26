package client

// urlEncodeBytes percent-encodes raw bytes using the form-urlencoded
// unreserved set (A-Za-z0-9 and *-._). All other bytes become %HH.
// upperHex selects between %AB and %ab output (per profile EncodedHexCase).
//
// The per-profile EncodingExclusionPattern is intentionally not honored —
// tracker fingerprinting expects this exact output for every profile.
func urlEncodeBytes(data []byte, upperHex bool) string {
	hex := "0123456789abcdef"
	if upperHex {
		hex = "0123456789ABCDEF"
	}
	out := make([]byte, 0, len(data)*3)
	for _, b := range data {
		if isFormUnreserved(b) {
			out = append(out, b)
			continue
		}
		out = append(out, '%', hex[b>>4], hex[b&0x0f])
	}
	return string(out)
}

// isFormUnreserved reports whether b is in the form-urlencoded unreserved
// set: A-Za-z0-9 plus '*', '-', '.', '_'. Note: '~' is NOT in this set
// (differs from RFC 3986 unreserved), matching Rust byte_serialize.
func isFormUnreserved(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z':
		return true
	case b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '*' || b == '-' || b == '.' || b == '_':
		return true
	}
	return false
}

