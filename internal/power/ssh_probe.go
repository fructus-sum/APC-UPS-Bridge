package power

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SSHProbeResult describes a non-destructive authentication check. The SSH
// connection sends no remote command and cannot validate shutdown permission.
type SSHProbeResult struct {
	Status  string `json:"status"` // ready, warning, error
	Message string `json:"message"`
}

// TestSSH checks key-based SSH authentication without opening a remote shell
// or requesting a remote command. This is essential: authorized_keys may set
// a forced command (including shutdown) that would override a harmless "true"
// or "id" probe. The -N option prevents a session/command request entirely.
//
// VERBOSE logging supplies OpenSSH's "Authenticated to ..." marker. A successful
// no-session SSH connection normally remains open; the bounded context stops it.
// No captured SSH output or credentials are returned to the browser.
func (e LinuxExecutor) TestSSH(ctx context.Context, d Device) SSHProbeResult {
	path := e.SSHPath
	if path == "" {
		path = "ssh"
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	args := []string{
		"-N", "-T",
		"-o", "BatchMode=yes",
		"-o", "PreferredAuthentications=publickey",
		"-o", "PasswordAuthentication=no",
		"-o", "KbdInteractiveAuthentication=no",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "NumberOfPasswordPrompts=0",
		"-o", "ConnectTimeout=5",
		"-o", "ConnectionAttempts=1",
		"-o", "LogLevel=VERBOSE",
		"-o", "PermitLocalCommand=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "RemoteCommand=none",
		"-p", strconv.Itoa(d.SSHPort),
		"--", d.SSHUser + "@" + d.Host,
	}
	output, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	// OpenSSH logs this only after SSH authentication and host-key checking.
	if strings.Contains(string(output), "Authenticated to ") {
		if err != nil && ctx.Err() == nil {
			return SSHProbeResult{Status: "warning", Message: "SSH authenticated, but the connection ended unexpectedly. No command was sent; check SSH server restrictions."}
		}
		return SSHProbeResult{Status: "ready", Message: "SSH public-key login and host-key checking succeeded. No remote command was sent. Shutdown permission was NOT verified."}
	}
	return SSHProbeResult{Status: "error", Message: "SSH login could not be verified. Check the saved destination, trusted host key and the Bridge service user's SSH identity. No remote command was sent."}
}
