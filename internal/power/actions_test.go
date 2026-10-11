package power

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestWakePacketSentToSelectedDestination(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	d := testDevice()
	d.WakeAddress = "127.0.0.1"
	d.WakePort = listener.LocalAddr().(*net.UDPAddr).Port
	if err := (LinuxExecutor{}).Wake(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 128)
	n, _, err := listener.ReadFromUDP(buf)
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := MagicPacket(d.MAC)
	if n != len(expected) {
		t.Fatalf("unexpected UDP length %d", n)
	}
	for i := range expected {
		if expected[i] != buf[i] {
			t.Fatalf("mismatched packet byte %d", i)
		}
	}
}

func TestSSHShutdownDroppedClassification(t *testing.T) {
	cases := []struct {
		name   string
		code   int
		output string
		want   bool
	}{
		{"closed by remote host", 255, "Connection to 10.20.30.11 closed by remote host.\n", true},
		{"crlf line ending", 255, "Connection to node-1.lan closed by remote host.\r\n", true},
		{"after warning line", 255, "Warning: Permanently added host\nConnection to 10.20.30.11 closed by remote host.\n", true},
		{"no trailing newline", 255, "Connection to 10.20.30.11 closed by remote host.", true},
		{"pre-auth kex drop", 255, "kex_exchange_identification: Connection closed by remote host\n", false},
		{"pre-auth kex drop with banner", 255, "kex_exchange_identification: read: Connection reset by peer\nConnection closed by 10.0.0.5 port 22\n", false},
		{"pre-auth close by address", 255, "Connection closed by 10.0.0.5 port 22\n", false},
		{"unanchored text", 255, "ssh: Connection to 10.20.30.11 closed by remote host.\n", false},
		{"mixed case", 255, "CONNECTION CLOSED BY REMOTE HOST", false},
		{"other ssh failure", 255, "ssh: connect to host 10.20.30.11 port 22: Connection refused\n", false},
		{"permission denied", 255, "root@10.20.30.11: Permission denied (publickey).\n", false},
		{"wrong exit code", 1, "Connection to 10.20.30.11 closed by remote host.\n", false},
		{"no exit code", -1, "closed by remote host", false},
		{"empty output", 255, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sshShutdownDropped(tc.code, []byte(tc.output)); got != tc.want {
				t.Fatalf("sshShutdownDropped(%d, %q) = %v, want %v", tc.code, tc.output, got, tc.want)
			}
		})
	}
}
