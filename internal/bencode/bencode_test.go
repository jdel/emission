package bencode

import (
	"bytes"
	"testing"
)

func TestDecodeInt(t *testing.T) {
	v, err := Decode([]byte("i42e"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != KindInt || v.Int != 42 {
		t.Errorf("got %+v", v)
	}
}

func TestDecodeBytes(t *testing.T) {
	v, err := Decode([]byte("5:hello"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != KindBytes || !bytes.Equal(v.Bytes, []byte("hello")) {
		t.Errorf("got %+v", v)
	}
}

func TestDecodeList(t *testing.T) {
	v, err := Decode([]byte("li1ei2e3:abce"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != KindList || len(v.List) != 3 {
		t.Fatalf("got %+v", v)
	}
	if v.List[0].Int != 1 || v.List[1].Int != 2 || string(v.List[2].Bytes) != "abc" {
		t.Errorf("got list %+v", v.List)
	}
}

func TestDecodeDictAndRawSlice(t *testing.T) {
	input := []byte("d3:bar3:baz3:fooi1ee")
	// keys "bar" -> "baz", "foo" -> 1
	v, err := Decode(input)
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != KindDict {
		t.Fatalf("got %+v", v)
	}
	foo, ok := v.Dict["foo"]
	if !ok || foo.Int != 1 {
		t.Errorf("foo wrong: %+v", foo)
	}
	bar, ok := v.Dict["bar"]
	if !ok || string(bar.Bytes) != "baz" {
		t.Errorf("bar wrong: %+v", bar)
	}
	// raw slice of the dict spans the whole input
	if v.Start != 0 || v.End != len(input) {
		t.Errorf("dict raw range = [%d, %d), want [0, %d)", v.Start, v.End, len(input))
	}
}

func TestDecodeNestedRawSlice(t *testing.T) {
	// Outer dict { info: { len: 1 } }
	input := []byte("d4:infod6:lengthi1eee")
	v, err := Decode(input)
	if err != nil {
		t.Fatal(err)
	}
	info := v.Dict["info"]
	// "info" value is the inner dict d6:lengthi1ee
	want := "d6:lengthi1ee"
	if !bytes.Equal(input[info.Start:info.End], []byte(want)) {
		t.Errorf("info raw = %q, want %q", input[info.Start:info.End], want)
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name, input, wantSubstr string
	}{
		{"empty", "", "eof"},
		{"unknown leading byte", "x", "unexpected byte"},
		{"unterminated int", "i42", "unterminated integer"},
		{"bad integer", "ifooe", "bad integer"},
		{"bytes missing colon", "5hello", "missing colon"},
		{"bytes truncated", "5:ab", "truncated"},
		{"unterminated list", "li1e", "unterminated list"},
		{"unterminated dict", "d3:fooi1e", "unterminated dict"},
		{"non-string dict key", "di1ei2ee", "dict key not a byte string"},
		{"bad bytes length", "-1:x", "unexpected byte"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Decode([]byte(c.input))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantSubstr)
			}
			if !bytes.Contains([]byte(err.Error()), []byte(c.wantSubstr)) {
				t.Errorf("err %q missing %q", err.Error(), c.wantSubstr)
			}
		})
	}
}

func TestDecodeIntEdgeCases(t *testing.T) {
	cases := map[string]int64{
		"i0e":                   0,
		"i-1e":                  -1,
		"i9223372036854775807e": 9223372036854775807, // max int64
	}
	for in, want := range cases {
		v, err := Decode([]byte(in))
		if err != nil {
			t.Errorf("Decode(%q) err = %v", in, err)
			continue
		}
		if v.Int != want {
			t.Errorf("Decode(%q) int = %d, want %d", in, v.Int, want)
		}
	}
}

func TestDecodeDepthLimit(t *testing.T) {
	// 1 million nested list openers — must return an error, not crash.
	payload := bytes.Repeat([]byte("l"), 1_000_000)
	_, err := Decode(payload)
	if err == nil {
		t.Fatal("expected error for deeply nested input, got nil")
	}
}

func TestDecodeEmptyContainers(t *testing.T) {
	v, err := Decode([]byte("le"))
	if err != nil || v.Kind != KindList || len(v.List) != 0 {
		t.Errorf("empty list: %+v err=%v", v, err)
	}
	v, err = Decode([]byte("de"))
	if err != nil || v.Kind != KindDict || len(v.Dict) != 0 {
		t.Errorf("empty dict: %+v err=%v", v, err)
	}
	v, err = Decode([]byte("0:"))
	if err != nil || v.Kind != KindBytes || len(v.Bytes) != 0 {
		t.Errorf("empty bytes: %+v err=%v", v, err)
	}
}

func TestDecodeRejectsOverflowingByteLength(t *testing.T) {
	// length = MaxInt64; d.pos+length overflows negative, skipping the
	// truncation guard and panicking the slice on the unfixed code.
	if _, err := Decode([]byte("9223372036854775807:abc")); err == nil {
		t.Fatal("want error for overflowing byte-string length, got nil")
	}
}
