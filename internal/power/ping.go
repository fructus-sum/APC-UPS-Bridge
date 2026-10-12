package power

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// ErrPingNotPermitted means the kernel refused to create an unprivileged ICMP
// datagram socket (see net.ipv4.ping_group_range).
var ErrPingNotPermitted = errors.New("ping is not permitted for the bridge service")

// ErrPingAddressFamily means the bridge host does not support the address
// family the device resolved to (for example IPv6 turned off on the bridge).
var ErrPingAddressFamily = errors.New("address type not supported by the bridge")

// ErrPingResolve means the host name could not be resolved to an address.
var ErrPingResolve = errors.New("host could not be resolved")

// ErrPingTimeout means no matching echo reply arrived in time.
var ErrPingTimeout = errors.New("no echo reply")

const pingTimeout = 2 * time.Second

const (
	icmpEchoRequest   = 8
	icmpEchoReply     = 0
	icmp6EchoRequest  = 128
	icmp6EchoReply    = 129
	icmpHeaderLen     = 8
	pingTokenLen      = 8
	pingResolveBudget = 3 * time.Second
)

// icmpChecksum is the Internet checksum (RFC 1071) over b.
func icmpChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// buildEchoRequest returns an ICMP echo request. The identifier is left zero
// because the kernel assigns it on datagram ICMP sockets; for ICMPv6 the kernel
// also computes the checksum, so it is only filled in for IPv4.
func buildEchoRequest(v6 bool, seq uint16, token []byte) []byte {
	p := make([]byte, icmpHeaderLen+len(token))
	p[0] = icmpEchoRequest
	if v6 {
		p[0] = icmp6EchoRequest
	}
	p[6], p[7] = byte(seq>>8), byte(seq)
	copy(p[icmpHeaderLen:], token)
	if !v6 {
		c := icmpChecksum(p)
		p[2], p[3] = byte(c>>8), byte(c)
	}
	return p
}

// isEchoReply reports whether pkt is an echo reply carrying exactly token.
// Anything else (other ICMP types, other payloads) is ignored by the caller.
func isEchoReply(v6 bool, pkt, token []byte) bool {
	if !v6 && len(pkt) >= 20 && pkt[0]>>4 == 4 {
		// Tolerate a leading IPv4 header; Linux ping sockets normally omit it.
		ihl := int(pkt[0]&0x0f) * 4
		if ihl < 20 || ihl > len(pkt) {
			return false
		}
		pkt = pkt[ihl:]
	}
	if len(pkt) < icmpHeaderLen {
		return false
	}
	want := byte(icmpEchoReply)
	if v6 {
		want = icmp6EchoReply
	}
	return pkt[0] == want && pkt[1] == 0 && bytes.Equal(pkt[icmpHeaderLen:], token)
}

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

// Ping implements the status monitor's prober for the real executor.
func (e LinuxExecutor) Ping(ctx context.Context, host string) (time.Duration, error) {
	return PingHost(ctx, host)
}
