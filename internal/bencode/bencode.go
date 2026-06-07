// Package bencode decodes BitTorrent bencode-encoded data.
//
// Supports the four bencode types: integers, byte strings, lists, and
// dictionaries. Each decoded [Value] records the raw byte range [Start, End)
// where it appeared in the input — useful to compute the info_hash, which is
// SHA-1 of the .torrent file's info dictionary in its original byte form.
//
// Reference: https://wiki.theory.org/BitTorrentSpecification#Bencoding
package bencode

import (
	"errors"
	"fmt"
	"strconv"
)

// Kind tags the type of a [Value].
type Kind int

const (
	KindInt Kind = iota
	KindBytes
	KindList
	KindDict
)

// Value is a decoded bencode value. Only the field corresponding to Kind is
// populated. Start and End are byte offsets into the input slice passed to
// Decode and bracket this value's encoded form (End exclusive).
type Value struct {
	Kind  Kind
	Int   int64
	Bytes []byte
	List  []Value
	Dict  map[string]Value
	Start int
	End   int
}

// Decode parses the first bencode value in data and returns it. Trailing
// bytes after the value are ignored (matching real-world .torrent files
// that may have minor garbage).
func Decode(data []byte) (Value, error) {
	d := &decoder{data: data}
	v, err := d.decode()
	if err != nil {
		return Value{}, err
	}
	return v, nil
}

const maxDepth = 100

type decoder struct {
	data  []byte
	pos   int
	depth int
}

func (d *decoder) decode() (Value, error) {
	if d.pos >= len(d.data) {
		return Value{}, errors.New("bencode: unexpected eof")
	}
	if d.depth > maxDepth {
		return Value{}, errors.New("bencode: nesting too deep")
	}
	start := d.pos
	switch c := d.data[d.pos]; {
	case c == 'i':
		return d.decodeInt(start)
	case c == 'l':
		return d.decodeList(start)
	case c == 'd':
		return d.decodeDict(start)
	case c >= '0' && c <= '9':
		return d.decodeBytes(start)
	default:
		return Value{}, fmt.Errorf("bencode: unexpected byte 0x%02x at offset %d", c, d.pos)
	}
}

func (d *decoder) decodeInt(start int) (Value, error) {
	d.pos++ // skip 'i'
	end := indexFrom(d.data, d.pos, 'e')
	if end < 0 {
		return Value{}, errors.New("bencode: unterminated integer")
	}
	n, err := strconv.ParseInt(string(d.data[d.pos:end]), 10, 64)
	if err != nil {
		return Value{}, fmt.Errorf("bencode: bad integer: %w", err)
	}
	d.pos = end + 1
	return Value{Kind: KindInt, Int: n, Start: start, End: d.pos}, nil
}

func (d *decoder) decodeBytes(start int) (Value, error) {
	colon := indexFrom(d.data, d.pos, ':')
	if colon < 0 {
		return Value{}, errors.New("bencode: bytes missing colon")
	}
	length, err := strconv.Atoi(string(d.data[d.pos:colon]))
	if err != nil || length < 0 {
		return Value{}, fmt.Errorf("bencode: bad bytes length")
	}
	d.pos = colon + 1
	if length > len(d.data)-d.pos { // subtraction can't overflow: d.pos <= len(d.data)
		return Value{}, errors.New("bencode: bytes truncated")
	}
	b := d.data[d.pos : d.pos+length]
	d.pos += length
	return Value{Kind: KindBytes, Bytes: b, Start: start, End: d.pos}, nil
}

func (d *decoder) decodeList(start int) (Value, error) {
	d.depth++
	defer func() { d.depth-- }()
	d.pos++ // skip 'l'
	var items []Value
	for d.pos < len(d.data) && d.data[d.pos] != 'e' {
		v, err := d.decode()
		if err != nil {
			return Value{}, err
		}
		items = append(items, v)
	}
	if d.pos >= len(d.data) {
		return Value{}, errors.New("bencode: unterminated list")
	}
	d.pos++ // skip 'e'
	return Value{Kind: KindList, List: items, Start: start, End: d.pos}, nil
}

func (d *decoder) decodeDict(start int) (Value, error) {
	d.depth++
	defer func() { d.depth-- }()
	d.pos++ // skip 'd'
	m := make(map[string]Value)
	for d.pos < len(d.data) && d.data[d.pos] != 'e' {
		k, err := d.decode()
		if err != nil {
			return Value{}, err
		}
		if k.Kind != KindBytes {
			return Value{}, errors.New("bencode: dict key not a byte string")
		}
		v, err := d.decode()
		if err != nil {
			return Value{}, err
		}
		m[string(k.Bytes)] = v
	}
	if d.pos >= len(d.data) {
		return Value{}, errors.New("bencode: unterminated dict")
	}
	d.pos++ // skip 'e'
	return Value{Kind: KindDict, Dict: m, Start: start, End: d.pos}, nil
}

func indexFrom(b []byte, from int, target byte) int {
	for i := from; i < len(b); i++ {
		if b[i] == target {
			return i
		}
	}
	return -1
}
