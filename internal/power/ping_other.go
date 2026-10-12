//go:build !linux

package power

import (
	"context"
	"time"
)

// PingHost needs Linux unprivileged ICMP sockets; elsewhere it is unavailable.
func PingHost(ctx context.Context, host string) (time.Duration, error) {
	return 0, ErrPingNotPermitted
}
