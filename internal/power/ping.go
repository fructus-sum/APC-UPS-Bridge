package power

import (
	"bytes"
	"context"
	"errors"
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

// Ping implements the status monitor's prober for the real executor.
func (e LinuxExecutor) Ping(ctx context.Context, host string) (time.Duration, error) {
	return PingHost(ctx, host)
}
