package client

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed clients/*.json
var clientsFS embed.FS

// Versions returns the sorted list of embedded client profile names — the
// valid arguments to [New]. Each name has the form "<client>-<version>",
// e.g. "transmission-4.0.6", "transmission-3.00".
func Versions() []string {
	entries, err := fs.ReadDir(clientsFS, "clients")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(out)
	return out
}

// loadProfile reads and decodes the embedded clients/<version>.json file.
// Returns an error wrapping fs.ErrNotExist if version is not embedded.
func loadProfile(version string) (*Profile, error) {
	data, err := clientsFS.ReadFile("clients/" + version + ".json")
	if err != nil {
		return nil, fmt.Errorf("unknown client version %q: %w", version, err)
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile %q: %w", version, err)
	}
	return &p, nil
}
