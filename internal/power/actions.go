package power

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

type Executor interface {
	Shutdown(context.Context, Device) error
	Wake(context.Context, Device) error
}

// Commands use fixed executable arguments; the browser cannot supply a script.
type LinuxExecutor struct{ SSHPath string }

func (e LinuxExecutor) Shutdown(ctx context.Context, d Device) error {
	path := e.SSHPath
	if path == "" {
		path = "ssh"
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	args := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=5", "-o", "ConnectionAttempts=1", "-p", strconv.Itoa(d.SSHPort), "--", d.SSHUser + "@" + d.Host}
	if d.UseSudo {
		args = append(args, "sudo", "-n", "shutdown", "-h", "now")
	} else {
		args = append(args, "shutdown", "-h", "now")
	}
	cmd := exec.CommandContext(ctx, path, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("SSH shutdown failed: %w (%s)", err, limitedOutput(output))
	}
	return nil
}
func limitedOutput(b []byte) string {
	if len(b) > 300 {
		b = b[:300]
	}
	return string(b)
}

// MagicPacket is a standard 102-byte Wake-on-LAN UDP payload.
func MagicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return nil, fmt.Errorf("invalid MAC")
	}
	data := make([]byte, 102)
	for i := 0; i < 6; i++ {
		data[i] = 0xff
	}
	for i := 0; i < 16; i++ {
		copy(data[6+i*6:], hw)
	}
	return data, nil
}
func (e LinuxExecutor) Wake(ctx context.Context, d Device) error {
	data, err := MagicPacket(d.MAC)
	if err != nil {
		return err
	}
	addr := d.WakeAddress
	if addr == "" {
		addr = "255.255.255.255"
	}
	port := d.WakePort
	if port == 0 {
		port = 9
	}
	dst := &net.UDPAddr{IP: net.ParseIP(addr).To4(), Port: port}
	if dst.IP == nil {
		return fmt.Errorf("invalid WoL destination")
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var sockerr error
	if err = raw.Control(func(fd uintptr) {
		sockerr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	}); err != nil {
		return err
	}
	if sockerr != nil {
		return sockerr
	}
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	_, err = conn.WriteToUDP(data, dst)
	return err
}
