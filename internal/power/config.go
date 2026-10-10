// Package power implements opt-in, persistent, fail-closed remote power actions.
package power

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Device identifies an explicitly approved Linux host. No arbitrary shell commands
// or SSH passwords are accepted. Remote shutdown uses an immutable command.
type Device struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Host            string `json:"host"`
	SSHUser         string `json:"ssh_user"`
	SSHPort         int    `json:"ssh_port"`
	UseSudo         bool   `json:"use_sudo"`
	MAC             string `json:"mac"`
	WakeAddress     string `json:"wake_address"`
	WakePort        int    `json:"wake_port"`
	ShutdownEnabled bool   `json:"shutdown_enabled"`
	WakeEnabled     bool   `json:"wake_enabled"`
	Priority        int    `json:"priority"`
}

type Config struct {
	Enabled                bool     `json:"enabled"`
	ShutdownBatteryPercent float64  `json:"shutdown_battery_percent"`
	ShutdownRuntimeMinutes float64  `json:"shutdown_runtime_minutes"` // 0 disables runtime rule
	OnBatteryDelaySeconds  int      `json:"on_battery_delay_seconds"`
	RecoveryBatteryPercent float64  `json:"recovery_battery_percent"`
	RecoveryStableSeconds  int      `json:"recovery_stable_seconds"`
	Devices                []Device `json:"devices"`
}

func Defaults() Config {
	return Config{ShutdownBatteryPercent: 20, OnBatteryDelaySeconds: 30, RecoveryBatteryPercent: 40, RecoveryStableSeconds: 120, Devices: []Device{}}
}

var idRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,39}$`)
var hostRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]{0,252}$`)
var userRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,31}$`)

func (c Config) Validate() error {
	if c.ShutdownBatteryPercent < 1 || c.ShutdownBatteryPercent > 95 ||
		c.RecoveryBatteryPercent <= c.ShutdownBatteryPercent || c.RecoveryBatteryPercent > 100 {
		return fmt.Errorf("battery thresholds invalid: recovery must exceed shutdown (1–95%%)")
	}
	if c.ShutdownRuntimeMinutes < 0 || c.ShutdownRuntimeMinutes > 180 ||
		c.OnBatteryDelaySeconds < 10 || c.OnBatteryDelaySeconds > 3600 ||
		c.RecoveryStableSeconds < 30 || c.RecoveryStableSeconds > 3600 {
		return fmt.Errorf("runtime or confirmation delays outside safe limits")
	}
	if len(c.Devices) > 16 {
		return fmt.Errorf("maximum 16 remote devices")
	}
	seen := map[string]bool{}
	active := false
	for _, d := range c.Devices {
		if !idRE.MatchString(d.ID) || seen[d.ID] || strings.TrimSpace(d.Name) == "" || len(d.Name) > 80 {
			return fmt.Errorf("invalid or duplicate device ID/name")
		}
		seen[d.ID] = true
		if !hostRE.MatchString(d.Host) || strings.Contains(d.Host, "..") || strings.HasSuffix(d.Host, ".") || !userRE.MatchString(d.SSHUser) || d.SSHPort < 1 || d.SSHPort > 65535 {
			return fmt.Errorf("device %s has invalid host, SSH user or port", d.ID)
		}
		if d.Priority < 0 || d.Priority > 100 {
			return fmt.Errorf("device %s has invalid shutdown priority", d.ID)
		}
		if d.ShutdownEnabled {
			active = true
		}
		if d.WakeEnabled && !d.ShutdownEnabled {
			return fmt.Errorf("device %s: wake requires shutdown enabled", d.ID)
		}
		if d.WakeEnabled {
			mac, err := net.ParseMAC(d.MAC)
			if err != nil || len(mac) != 6 {
				return fmt.Errorf("device %s: invalid six-byte MAC", d.ID)
			}
			address := d.WakeAddress
			if address == "" {
				address = "255.255.255.255"
			}
			ip := net.ParseIP(address)
			if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
				return fmt.Errorf("device %s: wake address must be an IPv4 broadcast address", d.ID)
			}
			if d.WakePort < 0 || d.WakePort > 65535 {
				return fmt.Errorf("device %s: invalid wake port", d.ID)
			}
		}
	}
	if c.Enabled && !active {
		return fmt.Errorf("enable at least one shutdown device before arming automation")
	}
	return nil
}

// Store atomically replaces the small JSON configuration. It does not create a daemon.
type Store struct {
	mu    sync.RWMutex
	path  string
	value Config
}

func OpenConfig(path string) (*Store, error) {
	s := &Store{path: path, value: Defaults()}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var v Config
	if err = json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("power configuration: %w", err)
	}
	if err = v.Validate(); err != nil {
		return nil, err
	}
	s.value = v
	return s, nil
}
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.value
	v.Devices = append([]Device(nil), v.Devices...)
	return v
}
func (s *Store) Save(v Config) error {
	if err := v.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = atomicWrite(s.path, append(b, '\n')); err != nil {
		return err
	}
	s.value = v
	return nil
}
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".power-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	// A rename alone does not guarantee durability across sudden power loss.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
