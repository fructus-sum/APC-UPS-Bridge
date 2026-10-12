//go:build !linux

package power

import "errors"

// enableBroadcast is only implemented on Linux, where the bridge runs.
func enableBroadcast(fd uintptr) error {
	return errors.New("broadcast is only supported on Linux")
}
