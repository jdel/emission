package file

import (
	"encoding/json"
	"os"

	"github.com/jdel/emission/internal/model"
)

// Settings persists the per-owner settings map as one JSON document at Path.
type Settings struct {
	Path string
}

func (s Settings) Load() (map[string]model.UserSettings, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	var m map[string]model.UserSettings
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s Settings) Save(m map[string]model.UserSettings) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.Path, data, 0o644)
}
