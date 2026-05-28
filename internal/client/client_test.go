package client

import (
	"strings"
	"testing"
)

func TestAllVersionsBuild(t *testing.T) {
	versions := Versions()
	if len(versions) < 50 {
		t.Fatalf("expected at least 50 versions, got %d", len(versions))
	}
	for _, v := range versions {
		t.Run(v, func(t *testing.T) {
			c, err := New(v)
			if err != nil {
				t.Fatalf("New(%q): %v", v, err)
			}
			if c.PeerID == "" {
				t.Errorf("empty peer id")
			}
			tmpl, headers := c.Query()
			must := []string{
				"info_hash={infohash}", "peer_id={peerid}",
				"uploaded={uploaded}", "downloaded={downloaded}",
				"left={left}", "key={key}", "event={event}",
			}
			for _, m := range must {
				if !strings.Contains(tmpl, m) {
					t.Errorf("query missing %q: %s", m, tmpl)
				}
			}
			if !strings.HasPrefix(v, "rtorrent") && !strings.Contains(tmpl, "numwant={numwant}") {
				t.Errorf("query missing numwant: %s", tmpl)
			}
			if strings.Contains(tmpl, "&&") {
				t.Errorf("query has empty arg: %s", tmpl)
			}
			if len(headers) == 0 {
				t.Errorf("no headers")
			}
		})
	}
}

func TestRegeneratePeerID(t *testing.T) {
	c, err := New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	first := c.PeerID
	for i := 0; i < 10; i++ {
		if err := c.GeneratePeerID(); err != nil {
			t.Fatal(err)
		}
		if c.PeerID != first {
			return
		}
	}
	t.Errorf("peer id did not change across 10 regenerations: %s", first)
}

func TestRegenerateKey(t *testing.T) {
	// Use a profile whose key has enough entropy for collisions to be
	// astronomically rare across 10 redraws.
	c, err := New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	first := c.Key
	for i := 0; i < 10; i++ {
		if err := c.GenerateKey(); err != nil {
			t.Fatal(err)
		}
		if c.Key != first {
			return
		}
	}
	t.Errorf("key did not change across 10 regenerations: %s", first)
}

func TestCloneFreshIdentity(t *testing.T) {
	c, err := New("transmission-4.0.6")
	if err != nil {
		t.Fatal(err)
	}
	n, err := c.Clone()
	if err != nil {
		t.Fatal(err)
	}
	if n.PeerID == c.PeerID {
		t.Error("clone reused peer id")
	}
	if n.Key == c.Key {
		t.Error("clone reused key")
	}
	if n.Version != c.Version || n.NumWant != c.NumWant || n.NumWantOnStop != c.NumWantOnStop {
		t.Error("clone changed profile settings")
	}
}

func TestGenFromPatternLatin1(t *testing.T) {
	// High codepoints must map to one byte each (Latin-1), not inflate to
	// multi-byte UTF-8 (which would leave a 0xc2/0xc3 signature).
	raw, err := genFromPattern("[\u008d\u00ad]{16}")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 16 {
		t.Fatalf("len = %d, want 16 (one byte per char)", len(raw))
	}
	for _, b := range raw {
		if b != 0x8d && b != 0xad {
			t.Errorf("byte %#x not in {0x8d,0xad}: UTF-8 inflation?", b)
		}
	}
}

func TestEphemeralPortRangeAndVaries(t *testing.T) {
	first := ephemeralPort()
	for i := 0; i < 20; i++ {
		p := ephemeralPort()
		if p < 49152 || p > 65534 {
			t.Fatalf("port %d outside ephemeral range 49152-65534", p)
		}
		if p != first {
			return
		}
	}
	t.Errorf("ephemeralPort returned %d for 21 consecutive draws", first)
}
