//go:build linux

package hostsettings

import (
	"os"
	"syscall"
)

// copyOwner gives f the same owner and group as the file described by original.
func copyOwner(f *os.File, original os.FileInfo) error {
	if sys, ok := original.Sys().(*syscall.Stat_t); ok {
		return f.Chown(int(sys.Uid), int(sys.Gid))
	}
	return nil
}
