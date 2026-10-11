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

const (
	// A failed remote shutdown is retried a bounded number of times, spaced by
	// shutdownRetryDelay, because it matters more that hosts are stopped before
	// the UPS battery is exhausted than that a command is never repeated.
	// Shutting down an already shutting-down host is idempotent.
	maxShutdownAttempts = 5
	shutdownRetryDelay  = 30 * time.Second
	// If mains is stable but the battery charge is unknown or never reaches the
	// recovery threshold, the cycle still completes after this long.
	recoveryBatteryTimeout = time.Hour
)

// State is deliberately small and durable: it is NOT a historical database.
// Every action is persisted as attempted BEFORE executing it, and the attempt
// counter is persisted with it, so retries stay bounded across crashes.
// Shutdown commands are retried (at most maxShutdownAttempts, one per
// shutdownRetryDelay) until one is accepted. A crash between intent and
// execution costs one attempt rather than silently skipping the host. Wake
// packets remain strictly at-most-once: a failed or lost WoL packet is never
// resent automatically.
type ActionState struct {
	ShutdownAttempted bool      `json:"shutdown_attempted"`
	ShutdownAttempts  int       `json:"shutdown_attempts,omitempty"`
	NextShutdownRetry time.Time `json:"next_shutdown_retry,omitempty"`
	ShutdownAccepted  bool      `json:"shutdown_accepted"`
	WakeAttempted     bool      `json:"wake_attempted"`
	WakeSent          bool      `json:"wake_sent"`
}

// shutdownAttemptCount treats legacy state (attempted, no counter) as one attempt.
func (a ActionState) shutdownAttemptCount() int {
	if a.ShutdownAttempts == 0 && a.ShutdownAttempted {
		return 1
	}
	return a.ShutdownAttempts
}

type State struct {
	Armed               bool                   `json:"armed"`
	Active              bool                   `json:"active"`
	StartedAt           time.Time              `json:"started_at,omitempty"`
	RecoverySince       time.Time              `json:"recovery_since,omitempty"`
	ShutdownTriggeredAt time.Time              `json:"shutdown_triggered_at,omitempty"`
	CycleConfig         Config                 `json:"cycle_config"`
	Actions             map[string]ActionState `json:"actions,omitempty"`
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
//
// Event log writes are best-effort: a failing log never aborts Observe, skips a
// state change or skips an executor call. The first event error is returned at
// the end so the caller can report it. State persistence errors still abort,
// because acting without a durable record of intent is unsafe.
func (c *Controller) Observe(ctx context.Context, s monitor.Status) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var eventErr error
	emit := func(typ, level string, data map[string]any) {
		if err := c.event(typ, level, data); err != nil && eventErr == nil {
			eventErr = err
		}
	}
	if err := c.observe(ctx, s, emit); err != nil {
		return err
	}
	if eventErr != nil {
		return fmt.Errorf("write power event: %w", eventErr)
	}
	return nil
}

func (c *Controller) observe(ctx context.Context, s monitor.Status, emit func(typ, level string, data map[string]any)) error {
	now := c.now().UTC()
	cfg := c.store.Get()
	if !cfg.Enabled {
		if c.state.Active || c.state.Armed {
			c.state = State{Actions: map[string]ActionState{}}
			if err := c.persist(); err != nil {
				return err
			}
			emit("power_automation_disarmed", "warning", nil)
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
			emit("power_recovery_confirmation_started", "info", nil)
			return nil
		}
		rules := c.state.CycleConfig
		stable := now.Sub(c.state.RecoverySince)
		if stable < time.Duration(rules.RecoveryStableSeconds)*time.Second || hasFlag(snap.Flags, "LOWBATT") {
			return nil
		}
		if snap.BatteryPercent == nil || *snap.BatteryPercent < rules.RecoveryBatteryPercent {
			// Keep waiting for the charge to recover, but do not wait forever: a UPS
			// that never reports (or never reaches) the threshold must not leave
			// hosts powered off indefinitely once mains has been stable for long.
			if stable < recoveryBatteryTimeout {
				return nil
			}
			details := map[string]any{"stable_seconds": int(stable.Seconds()), "required_battery_percent": rules.RecoveryBatteryPercent}
			if snap.BatteryPercent != nil {
				details["battery_percent"] = *snap.BatteryPercent
			}
			emit("power_recovery_battery_timeout", "warning", details)
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
			emit("remote_wake_attempt", "info", map[string]any{"device": d.ID})
			err := c.executor.Wake(ctx, d)
			if err == nil {
				a.WakeSent = true
			}
			c.state.Actions[d.ID] = a
			if saveErr := c.persist(); saveErr != nil {
				return saveErr
			}
			if err != nil {
				emit("remote_wake_failed", "warning", map[string]any{"device": d.ID, "reason": err.Error()})
			} else {
				emit("remote_wake_sent", "info", map[string]any{"device": d.ID})
			}
		}
		// Complete the cycle only after all eligible wake attempts are persisted.
		// Future ONLINE samples cannot resend magic packets after restart.
		c.state.Active = false
		c.state.StartedAt = time.Time{}
		c.state.RecoverySince = time.Time{}
		c.state.ShutdownTriggeredAt = time.Time{}
		c.state.CycleConfig = Config{}
		c.state.Actions = map[string]ActionState{}
		if err := c.persist(); err != nil {
			return err
		}
		emit("power_automation_cycle_completed", "info", nil)
		return nil
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
			c.state.ShutdownTriggeredAt = time.Time{}
			c.state.CycleConfig = cfg
			c.state.Actions = map[string]ActionState{}
			if err := c.persist(); err != nil {
				return err
			}
			emit("power_automation_cycle_started", "warning", nil)
			return nil
		}
		if !c.state.RecoverySince.IsZero() {
			c.state.RecoverySince = time.Time{}
			if err := c.persist(); err != nil {
				return err
			}
			emit("power_recovery_interrupted", "warning", nil)
		}
		rules := c.state.CycleConfig
		if now.Sub(c.state.StartedAt) < time.Duration(rules.OnBatteryDelaySeconds)*time.Second {
			return nil
		}
		// Once the shutdown condition has been met in this cycle it stays met: a
		// reading that flickers back above the threshold must not stop retries.
		if c.state.ShutdownTriggeredAt.IsZero() {
			batteryLow := snap.BatteryPercent != nil && *snap.BatteryPercent <= rules.ShutdownBatteryPercent
			runtimeLow := rules.ShutdownRuntimeMinutes > 0 && snap.RuntimeMinutes != nil && *snap.RuntimeMinutes <= rules.ShutdownRuntimeMinutes
			if !batteryLow && !runtimeLow && !hasFlag(snap.Flags, "LOWBATT") {
				return nil
			}
			c.state.ShutdownTriggeredAt = now
			if err := c.persist(); err != nil {
				return err
			}
		}
		ordered := append([]Device(nil), rules.Devices...)
		sortDevices(ordered)
		for _, d := range ordered {
			a := c.state.Actions[d.ID]
			attempts := a.shutdownAttemptCount()
			if !d.ShutdownEnabled || a.ShutdownAccepted || attempts >= maxShutdownAttempts || now.Before(a.NextShutdownRetry) || !stillApprovedShutdown(cfg, d) {
				continue
			}
			attempts++
			a.ShutdownAttempted = true
			a.ShutdownAttempts = attempts
			a.NextShutdownRetry = now.Add(shutdownRetryDelay)
			c.state.Actions[d.ID] = a
			if err := c.persist(); err != nil {
				return err
			}
			emit("remote_shutdown_attempt", "warning", map[string]any{"device": d.ID, "attempt": attempts, "max_attempts": maxShutdownAttempts})
			err := c.executor.Shutdown(ctx, d)
			if err == nil {
				a.ShutdownAccepted = true
			}
			c.state.Actions[d.ID] = a
			if saveErr := c.persist(); saveErr != nil {
				return saveErr
			}
			if err == nil {
				emit("remote_shutdown_accepted", "warning", map[string]any{"device": d.ID, "attempt": attempts})
				continue
			}
			emit("remote_shutdown_failed", "warning", map[string]any{"device": d.ID, "reason": err.Error(), "attempt": attempts, "max_attempts": maxShutdownAttempts})
			if attempts >= maxShutdownAttempts {
				emit("remote_shutdown_gave_up", "critical", map[string]any{"device": d.ID, "attempts": attempts, "reason": err.Error()})
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
