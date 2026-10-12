//go:build linux

package power

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func pingSocketError(err error) error {
	switch {
	case errors.Is(err, syscall.EAFNOSUPPORT):
		return ErrPingAddressFamily
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM),
		errors.Is(err, syscall.EPROTONOSUPPORT):
		return ErrPingNotPermitted
	}
	return fmt.Errorf("ping socket: %w", err)
}

// PingHost sends exactly one ICMP echo request through an unprivileged datagram
// socket and waits for the matching reply. The socket is always closed on
// return. It never opens a TCP or SSH connection.
func PingHost(ctx context.Context, host string) (time.Duration, error) {
	rctx, cancel := context.WithTimeout(ctx, pingResolveBudget)
	addrs, err := net.DefaultResolver.LookupIPAddr(rctx, host)
	cancel()
	if err != nil || len(addrs) == 0 {
		return 0, fmt.Errorf("%w: %s", ErrPingResolve, host)
	}
	target := addrs[0]
	for _, a := range addrs {
		if a.IP.To4() != nil {
			target = a
			break
		}
	}
	v6 := target.IP.To4() == nil
	var sa syscall.Sockaddr
	family, proto := syscall.AF_INET, syscall.IPPROTO_ICMP
	if v6 {
		family, proto = syscall.AF_INET6, syscall.IPPROTO_ICMPV6
		s6 := &syscall.SockaddrInet6{}
		copy(s6.Addr[:], target.IP.To16())
		if target.Zone != "" {
			if ifi, e := net.InterfaceByName(target.Zone); e == nil {
				s6.ZoneId = uint32(ifi.Index)
			}
		}
		sa = s6
	} else {
		s4 := &syscall.SockaddrInet4{}
		copy(s4.Addr[:], target.IP.To4())
		sa = s4
	}
	fd, err := syscall.Socket(family, syscall.SOCK_DGRAM|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, proto)
	if err != nil {
		return 0, pingSocketError(err)
	}
	// A connected socket also makes the kernel drop replies from other hosts.
	if err = syscall.Connect(fd, sa); err != nil {
		_ = syscall.Close(fd)
		return 0, fmt.Errorf("ping connect: %w", err)
	}
	f := os.NewFile(uintptr(fd), "icmp")
	defer f.Close()
	deadline := time.Now().Add(pingTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = f.SetDeadline(deadline); err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() { _ = f.SetDeadline(time.Now()) })
	defer stop()

	random := make([]byte, pingTokenLen+2)
	if _, err = rand.Read(random); err != nil {
		return 0, err
	}
	token := random[:pingTokenLen]
	seq := uint16(random[pingTokenLen])<<8 | uint16(random[pingTokenLen+1])
	start := time.Now()
	if _, err = f.Write(buildEchoRequest(v6, seq, token)); err != nil {
		return 0, fmt.Errorf("ping send: %w", err)
	}
	buf := make([]byte, 1500)
	for {
		n, err := f.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				if ctx.Err() != nil {
					return 0, ctx.Err()
				}
				return 0, ErrPingTimeout
			}
			return 0, fmt.Errorf("ping receive: %w", err)
		}
		if isEchoReply(v6, buf[:n], token) {
			return time.Since(start), nil
		}
	}
}
