package power

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SSHProbeResult describes a non-destructive preflight. A successful check is
// NOT proof that a future shutdown will be accepted or complete.
type SSHProbeResult struct {
	Status  string `json:"status"` // ready, warning, error
	Message string `json:"message"`
}

// This command uses only hard-coded shell syntax, never user-provided text.
// It checks authentication, host-key validation and whether the exact shutdown
// invocation may be allowed by sudo; it NEVER executes shutdown or changes
// power state. Some OS/sudo policies cannot be conclusively verified this way.
const sshPreflightCommand = `sh -c 'p=$(command -v shutdown 2>/dev/null || true); if [ -z "$p" ]; then for q in /usr/sbin/shutdown /sbin/shutdown /bin/shutdown; do if [ -x "$q" ]; then p="$q"; break; fi; done; fi; if [ -z "$p" ]; then echo NO_SHUTDOWN; elif [ "$(id -u)" = 0 ]; then echo ROOT_OK; elif command -v sudo >/dev/null 2>&1 && sudo -n -l "$p" -h now >/dev/null 2>&1; then echo SUDO_OK; else echo SSH_ONLY; fi'`

// TestSSH performs a single bounded, non-interactive connection as the current
// service user. Strict host-key checking remains enabled. No shutdown is sent.
func (e LinuxExecutor) TestSSH(ctx context.Context, d Device) SSHProbeResult {
	path := e.SSHPath
	if path == "" {
		path = "ssh"
	}
	ctx, cancel := context.WithTimeout(ctx, 9*time.Second)
	defer cancel()
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "NumberOfPasswordPrompts=0",
		"-o", "ConnectTimeout=5",
		"-o", "ConnectionAttempts=1",
		"-o", "UpdateHostKeys=no",
		"-p", strconv.Itoa(d.SSHPort),
		"--", d.SSHUser + "@" + d.Host, sshPreflightCommand,
	}
	out, err := exec.CommandContext(ctx, path, args...).Output()
	if err != nil {
		return SSHProbeResult{Status: "error", Message: "SSH connection or authentication failed. Check the destination, host key and the Bridge service user's SSH key."}
	}
	switch strings.TrimSpace(string(out)) {
	case "ROOT_OK":
		return SSHProbeResult{Status: "ready", Message: "SSH login succeeded as root and a shutdown executable is available. No shutdown was issued."}
	case "SUDO_OK":
		if !d.UseSudo {
			return SSHProbeResult{Status: "warning", Message: "SSH succeeded and sudo permission was found, but this device is not configured to use sudo. Enable 'Use sudo -n' for this device."}
		}
		return SSHProbeResult{Status: "ready", Message: "SSH succeeded and sudo reports permission for a non-interactive shutdown command. No shutdown was issued."}
	case "NO_SHUTDOWN":
		return SSHProbeResult{Status: "warning", Message: "SSH succeeded but a shutdown executable could not be found. No shutdown was issued."}
	case "SSH_ONLY":
		if d.UseSudo {
			return SSHProbeResult{Status: "warning", Message: "SSH succeeded, but non-interactive sudo shutdown permission could not be verified. No shutdown was issued."}
		}
		return SSHProbeResult{Status: "warning", Message: "SSH succeeded as a non-root user. The configured shutdown command may lack permission; use an authorized sudo rule if required."}
	default:
		return SSHProbeResult{Status: "warning", Message: "SSH connected, but the remote shutdown permission check was inconclusive. No shutdown was issued."}
	}
}
