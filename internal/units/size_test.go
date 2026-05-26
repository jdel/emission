package units

import "testing"

func TestParseRate(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		err  bool
	}{
		{"1024", 1024, false},
		{"1K", 1024, false},
		{"1k", 1024, false},
		{"2M", 2 << 20, false},
		{"1G", 1 << 30, false},
		{"1.5K", 1536, false},
		{"500KB", 500 << 10, false},
		{"500K/s", 500 << 10, false},
		{"500KB/s", 500 << 10, false},
		{"0", 0, false},
		{"", 0, true},
		{"-1K", 0, true},
		{"abc", 0, true},
		{"1Q", 0, true},
	}
	for _, c := range cases {
		got, err := ParseRate(c.in)
		if (err != nil) != c.err {
			t.Errorf("ParseRate(%q) err=%v want err=%v", c.in, err, c.err)
		}
		if err == nil && got != c.want {
			t.Errorf("ParseRate(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[uint64]string{
		0:             "0 B",
		512:           "512 B",
		1024:          "1.0 KB",
		1536:          "1.5 KB",
		1048576:       "1.0 MB",
		3 << 30:       "3.0 GB",
		1500000000000: "1.4 TB",
	}
	for n, want := range cases {
		if got := FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
