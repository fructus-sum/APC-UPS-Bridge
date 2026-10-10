package power

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
)

// State is deliberately small and durable: it is NOT a historical database.
// An action is persisted as attempted BEFORE executing it. This ensures at-most-
// once commands across crashes. A crash between intent and execution may miss
// an action, requiring operator intervention rather than a potentially unsafe retry.
type ActionState struct {
	ShutdownAttempted bool `json:"shutdown_attempted"`
	ShutdownAccepted  bool `json:"shutdown_accepted"`
	WakeAttempted     bool `json:"wake_attempted"`
	WakeSent          bool `json:"wake_sent"`
}
type State struct {
	Armed         bool                   `json:"armed"`
	Active        bool                   `json:"active"`
	StartedAt     time.Time              `json:"started_at,omitempty"`
	RecoverySince time.Time              `json:"recovery_since,omitempty"`
	CycleConfig   Config                 `json:"cycle_config"`
	Actions       map[string]ActionState `json:"actions,omitempty"`
}
type View struct {
	Enabled       bool                   `json:"enabled"`
	Armed         bool                   `json:"armed"`
	Active        bool                   `json:"active"`
	Phase         string                 `json:"phase"`
	StartedAt     time.Time              `json:"started_at,omitempty"`
	RecoverySince time.Time              `json:"recovery_since,omitempty"`
	Actions       map[string]ActionState `json:"actions"`
}

type Controller struct {
	mu       sync.Mutex
	store    *Store
	path     string
	writer   events.Writer
	executor Executor
	now      func() time.Time
	state    State
}

func OpenController(store *Store, statePath string, writer events.Writer, executor Executor) (*Controller, error) {
	if store == nil || writer == nil || executor == nil || statePath == "" {
		return nil, fmt.Errorf("power dependencies and state path required")
	}
	c := &Controller{store: store, path: statePath, writer: writer, executor: executor, now: time.Now, state: State{Actions: map[string]ActionState{}}}
	b, err := os.ReadFile(statePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(b, &c.state); err != nil {
			return nil, fmt.Errorf("invalid persisted power state (automation blocked): %w", err)
		}
		if c.state.Active {
			if c.state.StartedAt.IsZero() || c.state.CycleConfig.Validate() != nil {
				return nil, fmt.Errorf("invalid persisted outage state (automation blocked)")
			}
		}
		if c.state.Actions == nil {
			c.state.Actions = map[string]ActionState{}
		}
	}
	return c, nil
}
func (c *Controller) Config() Config { return c.store.Get() }

func (c *Controller) View() View {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := View{Enabled: c.store.Get().Enabled, Armed: c.state.Armed, Active: c.state.Active, Phase: "disabled", StartedAt: c.state.StartedAt, RecoverySince: c.state.RecoverySince, Actions: make(map[string]ActionState)}
	for id, a := range c.state.Actions {
		v.Actions[id] = a
	}
	if v.Enabled {
		v.Phase = "standby"
		if !v.Armed {
			v.Phase = "not_armed"
		}
	}
	if c.state.Active {
		v.Phase = "on_battery"
		if !c.state.RecoverySince.IsZero() {
			v.Phase = "waiting_for_recovery"
		}
	}
	return v
}
func (c *Controller) UpdateConfig(v Config) error {
	if err := v.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A running outage uses an immutable copy of its rules and device addresses.
	// Emergency global disarming is allowed, but in-flight rule editing is not.
	if c.state.Active && v.Enabled {
		return fmt.Errorf("power rules cannot be edited during an active outage; disable automation first")
	}
	return c.store.Save(v)
}
func (c *Controller) persist() error {
	data, err := json.MarshalIndent(c.state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(c.path, append(data, '\n'))
}
func (c *Controller) event(typ, level string, data map[string]any) error {
	return c.writer.Write(events.Event{Time: c.now().UTC(), Type: typ, Level: level, Details: data})
}

// Observe is run by the single monitor loop; it intentionally never uses stale
// LastGood values to make safety decisions. No work is done on UNKNOWN/failed polls.
func (c *Controller) Observe(ctx context.Context, s monitor.Status) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now().UTC()
	cfg := c.store.Get()
	if !cfg.Enabled {
		if c.state.Active || c.state.Armed {
			c.state = State{Actions: map[string]ActionState{}}
			if err := c.persist(); err != nil {
				return err
			}
			return c.event("power_automation_disarmed", "warning", nil)
		}
		return nil
	}
	if !s.Healthy || s.Current == nil || s.Current.Power == apc.Unknown {
		if c.state.Active && !c.state.RecoverySince.IsZero() {
			c.state.RecoverySince = time.Time{}
			return c.persist()
		}
		return nil
	}
	snap := s.Current
	switch snap.Power {
	case apc.Online:
		if !c.state.Active {
			if !c.state.Armed {
				c.state.Armed = true
				return c.persist()
			}
			return nil
		}
		if c.state.RecoverySince.IsZero() {
			c.state.RecoverySince = now
			if err := c.persist(); err != nil {
				return err
			}
			return c.event("power_recovery_confirmation_started", "info", nil)
		}
		rules := c.state.CycleConfig
		if now.Sub(c.state.RecoverySince) < time.Duration(rules.RecoveryStableSeconds)*time.Second || snap.BatteryPercent == nil || *snap.BatteryPercent < rules.RecoveryBatteryPercent || hasFlag(snap.Flags, "LOWBATT") {
			return nil
		}
		// Apply only to hosts whose shutdown returned successfully and which remain
		// approved with the same MAC/hostname in the current configuration.
		ordered := append([]Device(nil), rules.Devices...)
		sortDevices(ordered)
		for _, d := range ordered {
			a := c.state.Actions[d.ID]
			if !a.ShutdownAccepted || a.WakeAttempted || !d.WakeEnabled {
				continue
			}
			if !stillApproved(cfg, d) {
				continue
			}
			a.WakeAttempted = true
			c.state.Actions[d.ID] = a
			if err := c.persist(); err != nil {
				return err
			}
			if err := c.event("remote_wake_attempt", "info", map[string]any{"device": d.ID}); err != nil {
				return err
			}
			err := c.executor.Wake(ctx, d)
			if err == nil {
				a.WakeSent = true
			}
			c.state.Actions[d.ID] = a
			if saveErr := c.persist(); saveErr != nil {
				return saveErr
			}
			if err != nil {
				if e := c.event("remote_wake_failed", "warning", map[string]any{"device": d.ID, "reason": err.Error()}); e != nil {
					return e
				}
			} else if e := c.event("remote_wake_sent", "info", map[string]any{"device": d.ID}); e != nil {
				return e
			}
		}
		// Complete the cycle only after all eligible wake attempts are persisted.
		// Future ONLINE samples cannot resend magic packets after restart.
		c.state.Active = false
		c.state.StartedAt = time.Time{}
		c.state.RecoverySince = time.Time{}
		c.state.CycleConfig = Config{}
		c.state.Actions = map[string]ActionState{}
		if err := c.persist(); err != nil {
			return err
		}
		return c.event("power_automation_cycle_completed", "info", nil)
	case apc.OnBattery:
		if !c.state.Active {
			// A service booting into an outage without a prior armed ONLINE sample may
			// observe that state but must never spontaneously start shutdowns.
			if !c.state.Armed {
				return nil
			}
			c.state.Active = true
			c.state.StartedAt = now
			c.state.RecoverySince = time.Time{}
			c.state.CycleConfig = cfg
			c.state.Actions = map[string]ActionState{}
			if err := c.persist(); err != nil {
				return err
			}
			return c.event("power_automation_cycle_started", "warning", nil)
		}
		if !c.state.RecoverySince.IsZero() {
			c.state.RecoverySince = time.Time{}
			if err := c.persist(); err != nil {
				return err
			}
			if err := c.event("power_recovery_interrupted", "warning", nil); err != nil {
				return err
			}
		}
		rules := c.state.CycleConfig
		if now.Sub(c.state.StartedAt) < time.Duration(rules.OnBatteryDelaySeconds)*time.Second {
			return nil
		}
		batteryLow := snap.BatteryPercent != nil && *snap.BatteryPercent <= rules.ShutdownBatteryPercent
		runtimeLow := rules.ShutdownRuntimeMinutes > 0 && snap.RuntimeMinutes != nil && *snap.RuntimeMinutes <= rules.ShutdownRuntimeMinutes
		if !batteryLow && !runtimeLow && !hasFlag(snap.Flags, "LOWBATT") {
			return nil
		}
		ordered := append([]Device(nil), rules.Devices...)
		sortDevices(ordered)
		for _, d := range ordered {
			a := c.state.Actions[d.ID]
			if !d.ShutdownEnabled || a.ShutdownAttempted || !stillApprovedShutdown(cfg, d) {
				continue
			}
			a.ShutdownAttempted = true
			c.state.Actions[d.ID] = a
			if err := c.persist(); err != nil {
				return err
			}
			if err := c.event("remote_shutdown_attempt", "warning", map[string]any{"device": d.ID}); err != nil {
				return err
			}
			err := c.executor.Shutdown(ctx, d)
			if err == nil {
				a.ShutdownAccepted = true
			}
			c.state.Actions[d.ID] = a
			if saveErr := c.persist(); saveErr != nil {
				return saveErr
			}
			if err != nil {
				if e := c.event("remote_shutdown_failed", "warning", map[string]any{"device": d.ID, "reason": err.Error()}); e != nil {
					return e
				}
			} else if e := c.event("remote_shutdown_accepted", "warning", map[string]any{"device": d.ID}); e != nil {
				return e
			}
		}
	}
	return nil
}
func stillApproved(cfg Config, original Device) bool {
	for _, d := range cfg.Devices {
		if d.ID == original.ID && d.Host == original.Host && d.SSHUser == original.SSHUser && d.MAC == original.MAC && d.WakeAddress == original.WakeAddress && d.WakePort == original.WakePort && d.WakeEnabled {
			return true
		}
	}
	return false
}
func stillApprovedShutdown(cfg Config, original Device) bool {
	for _, d := range cfg.Devices {
		if d.ID == original.ID && d.Host == original.Host && d.SSHUser == original.SSHUser && d.SSHPort == original.SSHPort && d.UseSudo == original.UseSudo && d.ShutdownEnabled {
			return true
		}
	}
	return false
}
func hasFlag(flags []string, flag string) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}
func sortDevices(d []Device) {
	sort.SliceStable(d, func(i, j int) bool {
		if d[i].Priority == d[j].Priority {
			return d[i].ID < d[j].ID
		}
		return d[i].Priority < d[j].Priority
	})
}
