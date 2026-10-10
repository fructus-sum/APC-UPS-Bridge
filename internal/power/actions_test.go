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
