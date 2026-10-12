// Package fileutil holds the small file helpers shared by the stores that keep
// their state in JSON files.
package fileutil

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data so readers never see a partial file.
// It creates the parent directory (0700) if needed, writes a temporary file in
// the same directory with the given permissions, syncs it, renames it into
// place and syncs the directory so the rename survives sudden power loss.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	// A rename alone does not guarantee durability across sudden power loss.
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// WriteJSONAtomic stores v as indented JSON with a trailing newline.
func WriteJSONAtomic(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'), perm)
}

// ReadJSON decodes the JSON file at path into dst. A missing file is reported
// as found=false with no error. Other errors are returned unwrapped: a read
// failure has found=false, a decode failure has found=true, so callers can
// word the two differently.
func ReadJSON(path string, dst any) (found bool, err error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = json.Unmarshal(b, dst); err != nil {
		return true, err
	}
	return true, nil
}
