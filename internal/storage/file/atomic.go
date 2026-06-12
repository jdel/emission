// Package file implements the storage repositories on plain files: JSON
// documents written atomically (temp file + rename) and JSON-lines for
// append-heavy stats history.
package file

import (
	"os"
	"path/filepath"
)

// atomicWrite writes data to path atomically: a uniquely-named temp file in the
// same directory, then rename into place. The unique name lets concurrent
// writers to the same path proceed without clobbering each other's temp file.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after a successful rename
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil { // CreateTemp makes 0600
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
