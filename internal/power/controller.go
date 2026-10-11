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
	// An accepted shutdown is trusted only once the host stops answering on its
	// SSH port. The first check is made this long after the command was accepted.
	shutdownVerifyDelay = 2 * time.Minute
	// A host counts as down only after this many consecutive failed connection
	// checks, shutdownRetryDelay apart, so one dropped connection is not enough.
	requiredUnreachableChecks = 2
	// If mains is stable but the battery charge is unknown or never reaches the
	// recovery threshold, the cycle still completes after this long.
	recoveryBatteryTimeout = time.Hour
)

// State is deliberately small and durable: it is NOT a historical database.
// Every action is persisted as attempted BEFORE executing it, and the attempt
// counter is persisted with it, so retries stay bounded across crashes.
// Shutdown commands are retried (at most maxShutdownAttempts, one per
// shutdownRetryDelay) until one is accepted, and an accepted host that is still
// answering after shutdownVerifyDelay is sent another command. A crash between
// intent and execution costs one attempt rather than silently skipping the
// host. Wake packets remain strictly at-most-once: a failed or lost WoL packet
// is never resent automatically.
type ActionState struct {
	ShutdownAttempted bool      `json:"shutdown_attempted"`
	ShutdownAttempts  int       `json:"shutdown_attempts,omitempty"`
	NextShutdownRetry time.Time `json:"next_shutdown_retry,omitempty"`
	ShutdownAccepted  bool      `json:"shutdown_accepted"`
	LastShutdownAt    time.Time `json:"last_shutdown_at,omitempty"`
	ShutdownConfirmed bool      `json:"shutdown_confirmed,omitempty"`
	UnreachableChecks int       `json:"unreachable_checks,omitempty"`
	ShutdownGaveUp    bool      `json:"shutdown_gave_up,omitempty"`
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
	// observeMu serializes whole Observe calls. mu guards state and is never
	// held across executor I/O, so View and UpdateConfig stay responsive.
	observeMu sync.Mutex
	mu        sync.Mutex
	store     *Store
	path      string
	writer    events.Writer
	executor  Executor
	now       func() time.Time
	state     State
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
// Observe runs in three phases so that slow SSH/UDP I/O never blocks View or
// UpdateConfig: (a) under c.mu, decide what is due and persist the intent;
// (b) with no lock held, run the executor calls (concurrently within one
// priority group); (c) under c.mu again, record the results. observeMu keeps
// whole Observe calls from interleaving.
//
// Event log writes are best-effort: a failing log never aborts Observe, skips a
// state change or skips an executor call. The first event error is returned at
// the end so the caller can report it. State persistence errors still abort,
// because acting without a durable record of intent is unsafe.
func (c *Controller) Observe(ctx context.Context, s monitor.Status) error {
	c.observeMu.Lock()
	defer c.observeMu.Unlock()
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

type emitFunc func(typ, level string, data map[string]any)

// decision is the work chosen by the decide phase. Its intents are already
// persisted when it is returned.
type decision struct {
	startedAt  time.Time // identity of the power cycle the work belongs to
	doShutdown bool
	devices    []Device // priority-ordered devices of the frozen cycle
	wakes      []Device
}

type shutdownJob struct {
	dev    Device
	verify bool // liveness check instead of a shutdown command
}

type shutdownResult struct {
	err       error
	reachable bool
}

type pendingEvent struct {
	typ, level string
	data       map[string]any
}

func (c *Controller) locked(fn func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fn()
}

// cycleIs reports whether the power cycle that started at startedAt is still
// the live one. Callers hold c.mu.
func (c *Controller) cycleIs(startedAt time.Time) bool {
	return c.state.Active && c.state.StartedAt.Equal(startedAt)
}

func (c *Controller) observe(ctx context.Context, s monitor.Status, emit emitFunc) error {
	// A cancelled context means the service is stopping: the snapshot may be
	// stale and any command would fail, so do nothing and mutate no state.
	if err := ctx.Err(); err != nil {
		return err
	}
	var d decision
	if err := c.locked(func() error {
		var err error
		d, err = c.decide(s, emit)
		return err
	}); err != nil {
		return err
	}
	switch {
	case len(d.wakes) > 0:
		return c.runWakes(ctx, d, emit)
	case d.doShutdown:
		return c.runShutdowns(ctx, d, emit)
	}
	return nil
}

// decide applies the state machine to one sample. Callers hold c.mu; it
// performs no executor I/O.
func (c *Controller) decide(s monitor.Status, emit emitFunc) (decision, error) {
	now := c.now().UTC()
	cfg := c.store.Get()
	if !cfg.Enabled {
		if c.state.Active || c.state.Armed {
			c.state = State{Actions: map[string]ActionState{}}
			if err := c.persist(); err != nil {
				return decision{}, err
			}
			emit("power_automation_disarmed", "warning", nil)
		}
		return decision{}, nil
	}
	if !s.Healthy || s.Current == nil || s.Current.Power == apc.Unknown {
		if c.state.Active && !c.state.RecoverySince.IsZero() {
			c.state.RecoverySince = time.Time{}
			return decision{}, c.persist()
		}
		return decision{}, nil
	}
	snap := s.Current
	switch snap.Power {
	case apc.Online:
		if !c.state.Active {
			if !c.state.Armed {
				c.state.Armed = true
				return decision{}, c.persist()
			}
			return decision{}, nil
		}
		if c.state.RecoverySince.IsZero() {
			c.state.RecoverySince = now
			if err := c.persist(); err != nil {
				return decision{}, err
			}
			emit("power_recovery_confirmation_started", "info", nil)
			return decision{}, nil
		}
		rules := c.state.CycleConfig
		stable := now.Sub(c.state.RecoverySince)
		if stable < time.Duration(rules.RecoveryStableSeconds)*time.Second || hasFlag(snap.Flags, "LOWBATT") {
			return decision{}, nil
		}
		if snap.BatteryPercent == nil || *snap.BatteryPercent < rules.RecoveryBatteryPercent {
			// Keep waiting for the charge to recover, but do not wait forever: a UPS
			// that never reports (or never reaches) the threshold must not leave
			// hosts powered off indefinitely once mains has been stable for long.
			if stable < recoveryBatteryTimeout {
				return decision{}, nil
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
		var wakes []Device
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
			wakes = append(wakes, d)
		}
		if len(wakes) == 0 {
			return decision{}, c.completeCycle(emit)
		}
		// Wake intents are persisted before any packet is sent, so a crash can
		// never cause a second packet.
		if err := c.persist(); err != nil {
			return decision{}, err
		}
		for _, d := range wakes {
			emit("remote_wake_attempt", "info", map[string]any{"device": d.ID})
		}
		return decision{startedAt: c.state.StartedAt, wakes: wakes}, nil
	case apc.OnBattery:
		if !c.state.Active {
			// A service booting into an outage without a prior armed ONLINE sample may
			// observe that state but must never spontaneously start shutdowns.
			if !c.state.Armed {
				return decision{}, nil
			}
			c.state.Active = true
			c.state.StartedAt = now
			c.state.RecoverySince = time.Time{}
			c.state.ShutdownTriggeredAt = time.Time{}
			c.state.CycleConfig = cfg
			c.state.Actions = map[string]ActionState{}
			if err := c.persist(); err != nil {
				return decision{}, err
			}
			emit("power_automation_cycle_started", "warning", nil)
			return decision{}, nil
		}
		if !c.state.RecoverySince.IsZero() {
			c.state.RecoverySince = time.Time{}
			if err := c.persist(); err != nil {
				return decision{}, err
			}
			emit("power_recovery_interrupted", "warning", nil)
		}
		rules := c.state.CycleConfig
		if now.Sub(c.state.StartedAt) < time.Duration(rules.OnBatteryDelaySeconds)*time.Second {
			return decision{}, nil
		}
		// Once the shutdown condition has been met in this cycle it stays met: a
		// reading that flickers back above the threshold must not stop retries.
		if c.state.ShutdownTriggeredAt.IsZero() {
			batteryLow := snap.BatteryPercent != nil && *snap.BatteryPercent <= rules.ShutdownBatteryPercent
			runtimeLow := rules.ShutdownRuntimeMinutes > 0 && snap.RuntimeMinutes != nil && *snap.RuntimeMinutes <= rules.ShutdownRuntimeMinutes
			if !batteryLow && !runtimeLow && !hasFlag(snap.Flags, "LOWBATT") {
				return decision{}, nil
			}
			c.state.ShutdownTriggeredAt = now
			if err := c.persist(); err != nil {
				return decision{}, err
			}
		}
		ordered := append([]Device(nil), rules.Devices...)
		sortDevices(ordered)
		return decision{startedAt: c.state.StartedAt, doShutdown: true, devices: ordered}, nil
	}
	return decision{}, nil
}

// completeCycle ends the current outage cycle. Callers hold c.mu.
func (c *Controller) completeCycle(emit emitFunc) error {
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
}

// runParallel calls fn(i) for i in [0,n) concurrently and waits for all of them.
func runParallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			fn(i)
		}(i)
	}
	wg.Wait()
}

// runWakes sends the already-persisted wake intents without holding c.mu, then
// records the outcome and completes the cycle. Packets are never resent.
func (c *Controller) runWakes(ctx context.Context, d decision, emit emitFunc) error {
	errs := make([]error, len(d.wakes))
	runParallel(len(d.wakes), func(i int) { errs[i] = c.executor.Wake(ctx, d.wakes[i]) })
	return c.locked(func() error {
		if !c.cycleIs(d.startedAt) {
			return nil // disarmed while sending: never resurrect cleared state
		}
		for i, dev := range d.wakes {
			a := c.state.Actions[dev.ID]
			if errs[i] == nil {
				a.WakeSent = true
			}
			c.state.Actions[dev.ID] = a
		}
		if err := c.persist(); err != nil {
			return err
		}
		for i, dev := range d.wakes {
			if errs[i] != nil {
				emit("remote_wake_failed", "warning", map[string]any{"device": dev.ID, "reason": errs[i].Error()})
			} else {
				emit("remote_wake_sent", "info", map[string]any{"device": dev.ID})
			}
		}
		// Complete the cycle only after all eligible wake attempts are persisted.
		// Future ONLINE samples cannot resend magic packets after restart.
		return c.completeCycle(emit)
	})
}

// groupByPriority splits priority-ordered devices into runs of equal priority.
func groupByPriority(ordered []Device) [][]Device {
	var groups [][]Device
	for i, d := range ordered {
		if i == 0 || d.Priority != ordered[i-1].Priority {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], d)
	}
	return groups
}

// runShutdowns dispatches each priority group concurrently, waiting for one
// group's commands to return before starting the next.
func (c *Controller) runShutdowns(ctx context.Context, d decision, emit emitFunc) error {
	for _, group := range groupByPriority(d.devices) {
		proceed, err := c.shutdownGroup(ctx, d.startedAt, group, emit)
		if err != nil || !proceed {
			return err
		}
	}
	return nil
}

func (c *Controller) shutdownGroup(ctx context.Context, startedAt time.Time, group []Device, emit emitFunc) (bool, error) {
	var jobs []shutdownJob
	proceed := true
	err := c.locked(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		cfg := c.store.Get()
		if !cfg.Enabled || !c.cycleIs(startedAt) {
			proceed = false // disarmed or replaced meanwhile: take no further action
			return nil
		}
		var err error
		jobs, err = c.planShutdowns(group, cfg, c.now().UTC(), emit)
		return err
	})
	if err != nil || !proceed {
		return false, err
	}
	for len(jobs) > 0 {
		// Follow-up waves are planned under an earlier lock; re-check that the
		// admin has not disabled automation or changed the device meanwhile.
		err := c.locked(func() error {
			cfg := c.store.Get()
			if !cfg.Enabled || !c.cycleIs(startedAt) {
				proceed = false
				return nil
			}
			jobs = approvedJobs(cfg, jobs)
			return nil
		})
		if err != nil || !proceed || len(jobs) == 0 {
			return proceed, err
		}
		results := make([]shutdownResult, len(jobs))
		runParallel(len(jobs), func(i int) {
			if jobs[i].verify {
				results[i].reachable = c.executor.Reachable(ctx, jobs[i].dev)
			} else {
				results[i].err = c.executor.Shutdown(ctx, jobs[i].dev)
			}
		})
		done := jobs
		jobs = nil
		err = c.locked(func() error {
			cfg := c.store.Get()
			if !cfg.Enabled || !c.cycleIs(startedAt) {
				// Disarmed while commands were in flight: drop the results and
				// start nothing further, not even a resend.
				proceed = false
				return nil
			}
			var err error
			jobs, err = c.recordShutdowns(ctx, cfg, done, results, emit)
			return err
		})
		if err != nil || !proceed {
			return false, err
		}
	}
	return true, nil
}

// approvedJobs keeps the jobs whose device is still approved for shutdown.
func approvedJobs(cfg Config, jobs []shutdownJob) []shutdownJob {
	var out []shutdownJob
	for _, j := range jobs {
		if stillApprovedShutdown(cfg, j.dev) {
			out = append(out, j)
		}
	}
	return out
}

// planShutdowns chooses the due shutdown commands and liveness checks for one
// priority group and persists their intents. Callers hold c.mu.
func (c *Controller) planShutdowns(group []Device, cfg Config, now time.Time, emit emitFunc) ([]shutdownJob, error) {
	var jobs []shutdownJob
	var events []pendingEvent
	changed := false
	for _, d := range group {
		a := c.state.Actions[d.ID]
		if !d.ShutdownEnabled || a.ShutdownConfirmed || a.ShutdownGaveUp || !stillApprovedShutdown(cfg, d) {
			continue
		}
		attempts := a.shutdownAttemptCount()
		if !a.ShutdownAccepted && attempts >= maxShutdownAttempts {
			// Safety net for state saved before give-up was tracked.
			a.ShutdownGaveUp = true
			c.state.Actions[d.ID] = a
			changed = true
			events = append(events, pendingEvent{"remote_shutdown_gave_up", "critical", map[string]any{"device": d.ID, "attempts": attempts}})
			continue
		}
		if now.Before(a.NextShutdownRetry) {
			continue
		}
		if a.ShutdownAccepted {
			// The command was accepted; trust it only once the host is seen to be down.
			if now.Before(a.LastShutdownAt.Add(shutdownVerifyDelay)) {
				continue
			}
			jobs = append(jobs, shutdownJob{dev: d, verify: true})
			continue
		}
		jobs = append(jobs, c.beginShutdown(d, &events, now))
		changed = true
	}
	if changed {
		if err := c.persist(); err != nil {
			return nil, err
		}
	}
	for _, e := range events {
		emit(e.typ, e.level, e.data)
	}
	return jobs, nil
}

// beginShutdown records the intent to send one more shutdown command. Callers
// hold c.mu and persist afterwards, before any I/O.
func (c *Controller) beginShutdown(d Device, events *[]pendingEvent, now time.Time) shutdownJob {
	a := c.state.Actions[d.ID]
	attempts := a.shutdownAttemptCount() + 1
	a.ShutdownAttempted = true
	a.ShutdownAttempts = attempts
	a.NextShutdownRetry = now.Add(shutdownRetryDelay)
	c.state.Actions[d.ID] = a
	*events = append(*events, pendingEvent{"remote_shutdown_attempt", "warning", map[string]any{"device": d.ID, "attempt": attempts, "max_attempts": maxShutdownAttempts}})
	return shutdownJob{dev: d}
}

// recordShutdowns applies command and liveness results, persists them, and
// returns follow-up shutdown commands (already persisted as intents) for hosts
// that were accepted but are still running. Callers hold c.mu.
func (c *Controller) recordShutdowns(ctx context.Context, cfg Config, jobs []shutdownJob, results []shutdownResult, emit emitFunc) ([]shutdownJob, error) {
	if ctx.Err() != nil {
		// The service is stopping. Command outcomes and liveness checks made
		// under a cancelled context say nothing about the hosts, so record
		// none of them; give the interrupted commands their attempt back so
		// they are retried promptly after a restart.
		c.refundInterrupted(jobs)
		return nil, nil
	}
	now := c.now().UTC()
	var next []shutdownJob
	var events []pendingEvent
	for i, j := range jobs {
		id := j.dev.ID
		a := c.state.Actions[id]
		attempts := a.shutdownAttemptCount()
		r := results[i]
		switch {
		case j.verify:
			if !r.reachable {
				a.UnreachableChecks++
				if a.UnreachableChecks >= requiredUnreachableChecks {
					a.ShutdownConfirmed = true
					events = append(events, pendingEvent{"remote_shutdown_confirmed", "warning", map[string]any{"device": id}})
				} else {
					// One failed connection may be a network blip on the bridge's
					// side; look again before trusting it.
					a.NextShutdownRetry = now.Add(shutdownRetryDelay)
				}
				c.state.Actions[id] = a
				continue
			}
			a.UnreachableChecks = 0
			c.state.Actions[id] = a
			events = append(events, pendingEvent{"remote_shutdown_still_running", "warning", map[string]any{"device": id, "attempt": attempts, "max_attempts": maxShutdownAttempts}})
			if attempts >= maxShutdownAttempts {
				a.ShutdownGaveUp = true
				c.state.Actions[id] = a
				events = append(events, pendingEvent{"remote_shutdown_gave_up", "critical", map[string]any{"device": id, "attempts": attempts, "reason": "host still reachable"}})
				continue
			}
			if !stillApprovedShutdown(cfg, j.dev) {
				continue
			}
			next = append(next, c.beginShutdown(j.dev, &events, now))
		case r.err == nil:
			a.ShutdownAccepted = true
			a.LastShutdownAt = now
			a.UnreachableChecks = 0
			c.state.Actions[id] = a
			events = append(events, pendingEvent{"remote_shutdown_accepted", "warning", map[string]any{"device": id, "attempt": attempts}})
		default:
			events = append(events, pendingEvent{"remote_shutdown_failed", "warning", map[string]any{"device": id, "reason": r.err.Error(), "attempt": attempts, "max_attempts": maxShutdownAttempts}})
			if attempts >= maxShutdownAttempts {
				a.ShutdownGaveUp = true
				c.state.Actions[id] = a
				events = append(events, pendingEvent{"remote_shutdown_gave_up", "critical", map[string]any{"device": id, "attempts": attempts, "reason": r.err.Error()}})
			}
		}
	}
	if err := c.persist(); err != nil {
		return nil, err
	}
	for _, e := range events {
		emit(e.typ, e.level, e.data)
	}
	return next, nil
}

// refundInterrupted gives back the attempt consumed by shutdown commands that
// were cut short by cancellation. The refund is best-effort: if it cannot be
// persisted the attempt stays consumed on disk, which is the safe direction.
// Callers hold c.mu.
func (c *Controller) refundInterrupted(jobs []shutdownJob) {
	for _, j := range jobs {
		if j.verify {
			continue
		}
		a := c.state.Actions[j.dev.ID]
		n := a.shutdownAttemptCount() - 1
		if n < 0 {
			n = 0
		}
		a.ShutdownAttempts = n
		a.ShutdownAttempted = n > 0
		a.NextShutdownRetry = time.Time{}
		c.state.Actions[j.dev.ID] = a
	}
	_ = c.persist()
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
