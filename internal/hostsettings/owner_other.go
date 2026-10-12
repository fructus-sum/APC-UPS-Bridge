//go:build !linux

package hostsettings

import "os"

// copyOwner is a no-op where file ownership is not carried over.
func copyOwner(f *os.File, original os.FileInfo) error { return nil }
