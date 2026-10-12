package web

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/hostsettings"
	"net"
	"os"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

// hostShutdownSettings reports the effective configured apcupsd thresholds.
// Do not invent defaults when a setting or config file cannot be read.
func hostShutdownSettings() map[string]any {
	_, marked := os.Stat(hostsettings.ManagedMarker)
	t, err := hostsettings.ReadThresholds(hostsettings.ConfigPath)
	return map[string]any{
		"managed":         marked == nil,
		"source":          hostsettings.ConfigPath,
		"available":       err == nil,
		"battery_percent": intOrNil(t.BatteryPercent),
		"runtime_minutes": intOrNil(t.RuntimeMinutes),
		"timeout_seconds": intOrNil(t.TimeoutSeconds),
	}
}

func intOrNil(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func (h *Handler) testSavedSSH(ctx context.Context, id string) (power.SSHProbeResult, bool) {
	if h.Power == nil {
		return power.SSHProbeResult{}, false
	}
	for _, d := range h.Power.Config().Devices {
		if d.ID == id {
			return (power.LinuxExecutor{}).TestSSH(ctx, d), true
		}
	}
	return power.SSHProbeResult{}, false
}

func applyHostSettings(ctx context.Context, settings hostsettings.Settings) (hostsettings.Result, error) {
	c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", hostsettings.SocketPath)
	if err != nil {
		return hostsettings.Result{}, fmt.Errorf("check that the host settings socket is installed and enabled")
	}
	defer c.Close()
	deadline := time.Now().Add(50 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	if err := json.NewEncoder(c).Encode(settings); err != nil {
		return hostsettings.Result{}, fmt.Errorf("cannot send host settings")
	}
	var reply hostsettings.Result
	if err := json.NewDecoder(c).Decode(&reply); err != nil {
		return hostsettings.Result{}, fmt.Errorf("no response from host settings service; the change may or may not have been applied, refresh to check the current Pi thresholds")
	}
	return reply, nil
}
