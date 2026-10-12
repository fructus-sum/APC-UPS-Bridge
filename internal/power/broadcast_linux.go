//go:build linux

package power

import "syscall"

// enableBroadcast allows the UDP socket fd to send to a broadcast address.
func enableBroadcast(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
