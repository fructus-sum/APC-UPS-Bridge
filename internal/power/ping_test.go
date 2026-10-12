package power

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestICMPChecksumKnownVector(t *testing.T) {
	// RFC 1071 section 3 example: the words sum to 0xddf2, so the checksum is 0x220d.
	if got := icmpChecksum([]byte{0x00, 0x01, 0xf2, 0x03, 0xf4, 0xf5, 0xf6, 0xf7}); got != 0x220d {
		t.Fatalf("RFC 1071 vector: %#x", got)
	}
	// A packet with its checksum filled in sums to zero (odd length exercises padding).
	pkt := []byte{8, 0, 0, 0, 0x12, 0x34, 0, 1, 'a', 'b', 'c'}
	c := icmpChecksum(pkt)
	pkt[2], pkt[3] = byte(c>>8), byte(c)
	if icmpChecksum(pkt) != 0 {
		t.Fatalf("checksum does not verify: %#x", icmpChecksum(pkt))
	}
}

func TestBuildAndParseEcho(t *testing.T) {
	token := []byte("12345678")
	req := buildEchoRequest(false, 0x0102, token)
	if req[0] != 8 || req[1] != 0 || req[6] != 1 || req[7] != 2 || !bytes.Equal(req[8:], token) || icmpChecksum(req) != 0 {
		t.Fatalf("bad v4 request %v", req)
	}
	if r6 := buildEchoRequest(true, 1, token); r6[0] != 128 || r6[2] != 0 || r6[3] != 0 {
		t.Fatalf("bad v6 request %v", r6)
	}
	reply := append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, token...)
	if !isEchoReply(false, reply, token) {
		t.Fatal("valid reply rejected")
	}
	if isEchoReply(false, reply, []byte("87654321")) {
		t.Fatal("wrong token accepted")
	}
	if isEchoReply(false, req, token) {
		t.Fatal("echo request accepted as reply")
	}
	if isEchoReply(false, reply[:5], token) {
		t.Fatal("short packet accepted")
	}
	r6 := append([]byte{129, 0, 0, 0, 0, 0, 0, 1}, token...)
	if !isEchoReply(true, r6, token) || isEchoReply(false, r6, token) {
		t.Fatal("v6 reply type handling wrong")
	}
	withIP := append([]byte{0x45, 0, 0, 0, 0, 0, 0, 0, 64, 1, 0, 0, 127, 0, 0, 1, 127, 0, 0, 1}, reply...)
	if !isEchoReply(false, withIP, token) {
		t.Fatal("reply with IPv4 header rejected")
	}
}

func TestPingLoopback(t *testing.T) {
	rtt, err := PingHost(context.Background(), "127.0.0.1")
	if errors.Is(err, ErrPingNotPermitted) || errors.Is(err, ErrPingTimeout) {
		// Some sandboxes refuse ICMP sockets or drop loopback ICMP.
		t.Skipf("ICMP not usable in this environment: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if rtt <= 0 {
		t.Fatalf("rtt %v", rtt)
	}
}
