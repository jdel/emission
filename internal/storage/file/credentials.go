package file

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/jdel/emission/internal/model"
)

// Credentials persists the WebAuthn credential set as one JSON document at
// Path, written atomically and readable only by the owner.
type Credentials struct {
	Path string
}

func (c Credentials) Load() (model.CredentialSet, bool, error) {
	data, err := os.ReadFile(c.Path)
	if errors.Is(err, os.ErrNotExist) {
		return model.CredentialSet{}, false, nil
	}
	if err != nil {
		return model.CredentialSet{}, false, err
	}
	var cs model.CredentialSet
	if err := json.Unmarshal(data, &cs); err != nil {
		return model.CredentialSet{}, false, fmt.Errorf("parse passkey file %s: %w", c.Path, err)
	}
	return cs, true, nil
}

func (c Credentials) Save(cs model.CredentialSet) error {
	data, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(c.Path, data, 0o600)
}
