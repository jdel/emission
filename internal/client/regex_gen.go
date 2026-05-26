package client

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
)

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
			end := strings.IndexRune(string(runes[i:]), ']')
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
			end := strings.IndexRune(string(runes[i:]), '}')
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

// genFromPattern parses pattern and returns a UTF-8 byte sequence matching
// it. The length in bytes can exceed the regex's "char count" because
// codepoints >= 0x80 encode to 2+ bytes — callers must truncate to the
// required peer_id or key length after encoding.
func genFromPattern(pattern string) ([]byte, error) {
	atoms, err := parseRegex(pattern)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, a := range atoms {
		if len(a.chars) == 0 {
			continue
		}
		for n := 0; n < a.repeat; n++ {
			b.WriteRune(a.chars[rand.IntN(len(a.chars))])
		}
	}
	return []byte(b.String()), nil
}
