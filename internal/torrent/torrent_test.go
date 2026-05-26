package torrent

import (
	"fmt"
	"strings"
	"testing"
)

const pieces = "01234567890123456789" // exactly 20 bytes

// bstr renders s as a bencode byte string.
func bstr(s string) string { return fmt.Sprintf("%d:%s", len(s), s) }

// minimal valid single-file torrent:
//
//	announce -> http://t.example/a
//	info { length=1024 name=foo piece length=16384 pieces=20-byte-string }
func minimalTorrent(t *testing.T) []byte {
	t.Helper()
	return []byte("d" +
		bstr("announce") + bstr("http://t.example/a") +
		bstr("info") + "d" +
		bstr("length") + "i1024e" +
		bstr("name") + bstr("foo") +
		bstr("piece length") + "i16384e" +
		bstr("pieces") + bstr(pieces) +
		"e" +
		"e")
}

func TestParseMinimal(t *testing.T) {
	m, err := Parse(minimalTorrent(t))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "foo" {
		t.Errorf("name = %q", m.Name)
	}
	if m.Length != 1024 {
		t.Errorf("length = %d", m.Length)
	}
	if len(m.AnnounceURLs) != 1 || m.AnnounceURLs[0] != "http://t.example/a" {
		t.Errorf("urls = %v", m.AnnounceURLs)
	}
	if m.InfoHash == [20]byte{} {
		t.Error("info hash zero")
	}
	if m.InfoHashURLEncoded == "" {
		t.Error("info hash url-encoded empty")
	}
	if m.Private {
		t.Error("private should default to false")
	}
}

func TestParseMultiFile(t *testing.T) {
	// info { files: [{length=100 path=[a.dat]}, {length=200 path=[sub b.dat]}] }
	file1 := "d" + bstr("length") + "i100e" + bstr("path") + "l" + bstr("a.dat") + "e" + "e"
	file2 := "d" + bstr("length") + "i200e" + bstr("path") + "l" + bstr("sub") + bstr("b.dat") + "e" + "e"
	data := []byte("d" +
		bstr("announce") + bstr("http://t.example/a") +
		bstr("info") + "d" +
		bstr("files") + "l" + file1 + file2 + "e" +
		bstr("name") + bstr("bundle") +
		bstr("piece length") + "i16384e" +
		bstr("pieces") + bstr(pieces) +
		"e" +
		"e")

	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "bundle" {
		t.Errorf("name = %q", m.Name)
	}
	if m.Length != 300 {
		t.Errorf("length = %d, want 300", m.Length)
	}
}

func TestParsePrivateFlag(t *testing.T) {
	data := []byte("d" +
		bstr("announce") + bstr("http://t.example/a") +
		bstr("info") + "d" +
		bstr("length") + "i1024e" +
		bstr("name") + bstr("foo") +
		bstr("piece length") + "i16384e" +
		bstr("pieces") + bstr(pieces) +
		bstr("private") + "i1e" +
		"e" +
		"e")
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Private {
		t.Error("Private = false, want true")
	}
}

func TestParseAnnounceList(t *testing.T) {
	// Two tiers, with the second tier's first URL duplicating "announce".
	// Expected: dedup against the announce field, both unique URLs retained,
	// announce-list URLs come first (current implementation behaviour).
	tier1 := "l" + bstr("http://t1.example/a") + bstr("http://t2.example/a") + "e"
	tier2 := "l" + bstr("http://t.example/a") + bstr("http://t3.example/a") + "e"
	data := []byte("d" +
		bstr("announce") + bstr("http://t.example/a") +
		bstr("announce-list") + "l" + tier1 + tier2 + "e" +
		bstr("info") + "d" +
		bstr("length") + "i1024e" +
		bstr("name") + bstr("foo") +
		bstr("piece length") + "i16384e" +
		bstr("pieces") + bstr(pieces) +
		"e" +
		"e")
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"http://t1.example/a",
		"http://t2.example/a",
		"http://t.example/a",
		"http://t3.example/a",
	}
	if len(m.AnnounceURLs) != len(want) {
		t.Fatalf("urls = %v, want %d entries", m.AnnounceURLs, len(want))
	}
	for i, u := range want {
		if m.AnnounceURLs[i] != u {
			t.Errorf("urls[%d] = %q, want %q", i, m.AnnounceURLs[i], u)
		}
	}
}

func TestParseRejectsUDP(t *testing.T) {
	data := []byte("d" +
		bstr("announce") + bstr("udp://tracker.example:6969/a") +
		bstr("info") + "d" +
		bstr("length") + "i1e" +
		bstr("name") + bstr("x") +
		bstr("piece length") + "i16384e" +
		bstr("pieces") + bstr(pieces) +
		"e" +
		"e")
	_, err := Parse(data)
	if err == nil || !strings.Contains(err.Error(), "announce") {
		t.Fatalf("expected announce-URL rejection, got %v", err)
	}
}

func TestParseRejectsLocalHost(t *testing.T) {
	// Only announce URL is .local — should leave zero supported URLs.
	data := []byte("d" +
		bstr("announce") + bstr("http://emission.local/a") +
		bstr("info") + "d" +
		bstr("length") + "i1024e" +
		bstr("name") + bstr("foo") +
		bstr("piece length") + "i16384e" +
		bstr("pieces") + bstr(pieces) +
		"e" +
		"e")
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected .local-only torrent to be rejected")
	}
}

func TestParseMixedAnnounceListKeepsSupported(t *testing.T) {
	// announce-list mixes UDP, .local, and HTTPS — only HTTPS survives.
	tier := "l" + bstr("udp://tracker.example/a") + bstr("http://thing.local/a") + bstr("https://ok.example/a") + "e"
	data := []byte("d" +
		bstr("announce-list") + "l" + tier + "e" +
		bstr("info") + "d" +
		bstr("length") + "i1024e" +
		bstr("name") + bstr("foo") +
		bstr("piece length") + "i16384e" +
		bstr("pieces") + bstr(pieces) +
		"e" +
		"e")
	m, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.AnnounceURLs) != 1 || m.AnnounceURLs[0] != "https://ok.example/a" {
		t.Errorf("urls = %v, want [https://ok.example/a]", m.AnnounceURLs)
	}
}

func TestParseMissingInfo(t *testing.T) {
	data := []byte("d" + bstr("announce") + bstr("http://t.example/a") + "e")
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for missing info dict")
	}
}

func TestParseRootNotDict(t *testing.T) {
	if _, err := Parse([]byte("i42e")); err == nil {
		t.Fatal("expected error for non-dict root")
	}
}

func TestParseNoLengthOrFiles(t *testing.T) {
	// info present but neither length nor files.
	data := []byte("d" +
		bstr("announce") + bstr("http://t.example/a") +
		bstr("info") + "d" +
		bstr("name") + bstr("foo") +
		bstr("piece length") + "i16384e" +
		bstr("pieces") + bstr(pieces) +
		"e" +
		"e")
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for info without length or files")
	}
}
