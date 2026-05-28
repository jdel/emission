package client

import (
	"fmt"
	"math/rand/v2"
	"strconv"
)

// indexRune returns the index of the first occurrence of target in rs, or -1.
// Unlike strings.IndexRune it returns a rune offset (not a byte offset), so it
// is safe to use as a slice index into a []rune holding multi-byte runes.
func indexRune(rs []rune, target rune) int {
	for i, r := range rs {
		if r == target {
			return i
		}
	}
	return -1
}

// regexAtom is one node of the parsed regex. Each atom contributes between
// 1 and repeat runes to the output, drawn uniformly from chars.
//
// A literal "x" yields chars=['x'], repeat=1.
// A class "[a-c]" yields chars=['a','b','c'], repeat=1.
// A class with quantifier "[a-c]{3}" yields chars=['a','b','c'], repeat=3.
type regexAtom struct {
	chars  []rune
	repeat int
}

// parseRegex parses the limited regex dialect used by client profiles:
//   - literal chars (BMP runes incl. > 0x7f)
//   - \xHH escape (rare; JSON usually pre-decodes to a rune)
//   - escaped specials: \( \) \! \. \* \- \\
//   - char class [a-z A-Z 0-9 ...] with ranges, escaped specials, raw runes
//   - {N} repetition applied to previous atom
//   - grouping ( ... ) is treated as sequence (no alternation observed)
func parseRegex(pattern string) ([]regexAtom, error) {
	runes := []rune(pattern)
	var atoms []regexAtom
	i := 0
	for i < len(runes) {
		r := runes[i]
		switch r {
		case '(':
			i++
		case ')':
			i++
		case '[':
			end := indexRune(runes[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unclosed [ in %q", pattern)
			}
			class := runes[i+1 : i+end]
			chars, err := parseClass(class)
			if err != nil {
				return nil, err
			}
			atoms = append(atoms, regexAtom{chars: chars, repeat: 1})
			i += end + 1
		case '{':
			end := indexRune(runes[i:], '}')
			if end < 0 {
				return nil, fmt.Errorf("unclosed { in %q", pattern)
			}
			n, err := strconv.Atoi(string(runes[i+1 : i+end]))
			if err != nil || len(atoms) == 0 {
				return nil, fmt.Errorf("bad {N} in %q", pattern)
			}
			atoms[len(atoms)-1].repeat = n
			i += end + 1
		case '\\':
			if i+1 >= len(runes) {
				return nil, fmt.Errorf("trailing \\ in %q", pattern)
			}
			next := runes[i+1]
			if next == 'x' && i+3 < len(runes) {
				v, err := strconv.ParseInt(string(runes[i+2:i+4]), 16, 32)
				if err != nil {
					return nil, fmt.Errorf("bad \\xHH in %q", pattern)
				}
				atoms = append(atoms, regexAtom{chars: []rune{rune(v)}, repeat: 1})
				i += 4
			} else {
				atoms = append(atoms, regexAtom{chars: []rune{next}, repeat: 1})
				i += 2
			}
		default:
			atoms = append(atoms, regexAtom{chars: []rune{r}, repeat: 1})
			i++
		}
	}
	return atoms, nil
}

// parseClass expands a regex character class body (the runes between '[' and
// ']') into the flat list of runes it accepts. Supports a-b ranges (including
// \xHH endpoints), \xHH escapes, and backslash-escaped literals.
func parseClass(class []rune) ([]rune, error) {
	var out []rune
	i := 0
	for i < len(class) {
		r := class[i]
		// escape
		if r == '\\' && i+1 < len(class) {
			next := class[i+1]
			if next == 'x' && i+3 < len(class) {
				v, err := strconv.ParseInt(string(class[i+2:i+4]), 16, 32)
				if err != nil {
					return nil, fmt.Errorf("bad \\xHH in class")
				}
				out = append(out, rune(v))
				i += 4
				continue
			}
			out = append(out, next)
			i += 2
			continue
		}
		// range a-b (but not if '-' is at the end)
		if i+2 < len(class) && class[i+1] == '-' && class[i+2] != ']' {
			lo := r
			hi := class[i+2]
			step := 3
			if hi == '\\' && i+4 < len(class) && class[i+3] == 'x' {
				v, err := strconv.ParseInt(string(class[i+4:i+6]), 16, 32)
				if err != nil {
					return nil, fmt.Errorf("bad \\xHH range end")
				}
				hi = rune(v)
				step = 6
			}
			for c := lo; c <= hi; c++ {
				out = append(out, c)
			}
			i += step
			continue
		}
		out = append(out, r)
		i++
	}
	return out, nil
}

// genFromPattern parses pattern and returns the byte sequence it describes,
// one byte per matched character. peer_id and key are raw byte fields, and the
// profile patterns draw from codepoints 0x01–0xFF, so each character maps to a
// single Latin-1 byte — matching the on-wire layout real clients produce.
// Encoding as UTF-8 instead would inflate codepoints >= 0x80 to two bytes
// (e.g. 0x8d -> 0xc2 0x8d), corrupting the byte values and leaving a 0xc2/0xc3
// signature no real client emits. Callers truncate to the required length.
func genFromPattern(pattern string) ([]byte, error) {
	atoms, err := parseRegex(pattern)
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, a := range atoms {
		if len(a.chars) == 0 {
			continue
		}
		for n := 0; n < a.repeat; n++ {
			out = append(out, byte(a.chars[rand.IntN(len(a.chars))]))
		}
	}
	return out, nil
}
