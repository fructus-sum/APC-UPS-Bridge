//go:build !windows

package fileutil

import "os"

// syncDir flushes a directory entry so a completed rename survives power loss.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
