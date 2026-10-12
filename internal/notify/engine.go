package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/fileutil"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
)

// alertCooldown is the minimum gap between attempted start/restore alerts within
// one incident, so flapping mains cannot flood the mailbox.
const alertCooldown = 10 * time.Minute

// Target identifies a system that must become SSH-reachable before the summary.
// Host identity is captured at outage time and survives Pi reboots.
type Target struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}
type Period struct {
	StartedAt      time.Time `json:"started_at"`
	EndedAt        time.Time `json:"ended_at,omitempty"`
	StartAttempted bool      `json:"start_attempted"`
	EndAttempted   bool      `json:"end_attempted"`
	UncertainStart bool      `json:"uncertain_start,omitempty"`
	// AlertSuppressed marks a period that began and ended inside the alert
	// cooldown, so no start or restore email was sent for it.
	AlertSuppressed bool `json:"alert_suppressed,omitempty"`
}
type Incident struct {
	ID                     string    `json:"id"`
	CreatedAt              time.Time `json:"created_at"`
	Periods                []Period  `json:"periods"`
	LowestBattery          *float64  `json:"lowest_battery,omitempty"`
	Targets                []Target  `json:"targets,omitempty"`
	PiRestarts             int       `json:"pi_restarts"`
	ObservationInterrupted bool      `json:"observation_interrupted,omitempty"`
	LastError              string    `json:"last_error,omitempty"`
	NextAttempt            time.Time `json:"next_attempt,omitempty"`
	// LastAlertAt is when a start or restore alert was last attempted; it
	// drives the per-incident alert cooldown against flapping mains.
	LastAlertAt     time.Time `json:"last_alert_at,omitempty"`
	SummaryBlocked  bool      `json:"summary_blocked,omitempty"`
	SummaryInFlight bool      `json:"summary_in_flight,omitempty"`
}
type State struct {
	SeenOnline   bool      `json:"seen_online"`
	Current      *Incident `json:"current,omitempty"`
	LastReportID string    `json:"last_report_id,omitempty"`
	LastReportAt time.Time `json:"last_report_at,omitempty"`
}
type Status struct {
	Enabled      bool      `json:"enabled"`
	Pending      bool      `json:"pending"`
	IncidentID   string    `json:"incident_id,omitempty"`
	Outages      int       `json:"outages"`
	PiRestarts   int       `json:"pi_restarts"`
	LastError    string    `json:"last_error,omitempty"`
	LastReportID string    `json:"last_report_id,omitempty"`
	LastReportAt time.Time `json:"last_report_at,omitempty"`
}
type Sample struct {
	UPS     monitor.Status
	Targets []Target
}

// Probe checks whether a mail relay is reachable without sending a message.
type Probe func(context.Context, Config) error
type HostCheck func(context.Context, []Target) bool

type Engine struct {
	mu                sync.Mutex
	store             *Store
	path              string
	logs              string
	writer            events.Writer
	sender            Sender
	probe             Probe
	hosts             HostCheck
	now               func() time.Time
	state             State
	lastProbe         time.Time
	lastSecurityAlert time.Time
}

func Open(store *Store, statePath, logDir string, writer events.Writer, sender Sender) (*Engine, error) {
	if store == nil || statePath == "" || writer == nil || sender == nil {
		return nil, fmt.Errorf("email engine missing dependencies")
	}
	e := &Engine{store: store, path: statePath, logs: logDir, writer: writer, sender: sender, probe: TCPProbe, hosts: checkHosts, now: time.Now}
	found, err := fileutil.ReadJSON(statePath, &e.state)
	if found && err != nil {
		err = fmt.Errorf("invalid saved email outage report: %w", err)
	}
	if err != nil {
		return nil, err
	}
	if e.state.Current != nil {
		if e.state.Current.ID == "" || len(e.state.Current.Periods) == 0 {
			return nil, fmt.Errorf("invalid saved email incident")
		}
		e.state.Current.PiRestarts++
		e.state.Current.ObservationInterrupted = true
		// A Pi may reboot while SMTP is in flight. Reuse the same Message-ID if
		// sending later; SMTP cannot guarantee exactly-once delivery.
		e.state.Current.SummaryInFlight = false
		if err = e.persist(); err != nil {
			return nil, err
		}
	}
	return e, nil
}
func (e *Engine) View() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := Status{Enabled: e.store.Get().Enabled, LastReportID: e.state.LastReportID, LastReportAt: e.state.LastReportAt}
	if i := e.state.Current; i != nil {
		result.Pending = true
		result.IncidentID = i.ID
		result.Outages = len(i.Periods)
		result.PiRestarts = i.PiRestarts
		result.LastError = i.LastError
	}
	return result
}
func (e *Engine) Config() View { return e.store.Get().View() }
func (e *Engine) SaveConfig(c Config) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.store.Save(c); err != nil {
		return err
	}
	if e.state.Current != nil {
		e.state.Current.SummaryBlocked = false
		e.state.Current.NextAttempt = time.Time{}
		return e.persist()
	}
	return nil
}
func (e *Engine) persist() error {
	return fileutil.WriteJSONAtomic(e.path, e.state, 0600)
}
func (e *Engine) log(typ, level string, details map[string]any) {
	_ = e.writer.Write(events.Event{Time: e.now().UTC(), Type: typ, Level: level, Details: details})
}
func makeID(now time.Time) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return now.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

// Observe records transitions. Do not call concurrently; the worker processes
// one sample at a time. SMTP failures never propagate into UPS protection.
func (e *Engine) Observe(ctx context.Context, s monitor.Status, targets []Target) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	cfg := e.store.Get()
	if !cfg.Enabled {
		return nil
	}
	if !s.Healthy || s.Current == nil || s.Current.Power == apc.Unknown {
		return nil
	}
	now := e.now().UTC()
	up := s.Current
	if up.Power == apc.OnBattery {
		if e.state.Current == nil {
			e.state.Current = &Incident{ID: makeID(now), CreatedAt: now, Targets: append([]Target(nil), targets...)}
		}
		i := e.state.Current
		if len(i.Periods) == 0 || !i.Periods[len(i.Periods)-1].EndedAt.IsZero() {
			i.Periods = append(i.Periods, Period{StartedAt: now, UncertainStart: !e.state.SeenOnline})
			if err := e.persist(); err != nil {
				return err
			}
			e.log("email_outage_recorded", "warning", map[string]any{"incident": i.ID, "segment": len(i.Periods)})
		}
		if up.BatteryPercent != nil && (i.LowestBattery == nil || *up.BatteryPercent < *i.LowestBattery) {
			old := i.LowestBattery
			v := *up.BatteryPercent
			i.LowestBattery = &v
			if old == nil || math.Floor(v) < math.Floor(*old) {
				if err := e.persist(); err != nil {
					return err
				}
			}
		}
		p := &i.Periods[len(i.Periods)-1]
		if !p.StartAttempted {
			// Flapping mains: wait out the cooldown after the previous alert. If
			// the outage ends first, no alert is sent for it; if it persists, the
			// start alert goes out on the first sample after the cooldown.
			if !i.LastAlertAt.IsZero() && now.Sub(i.LastAlertAt) < alertCooldown {
				return nil
			}
			p.StartAttempted = true
			i.LastAlertAt = now
			if err := e.persist(); err != nil {
				return err
			}
			subject := "APC UPS Bridge — Power failure detected"
			if p.UncertainStart {
				subject = "APC UPS Bridge — UPS on battery (start time unknown)"
			}
			e.attempt(ctx, cfg, subject, fmt.Sprintf("Incident: %s\nUPS power: ON BATTERY\nDetected: %s\nBattery: %s\n\nThis alert is sent once and is not retried if the internet is down. Further short interruptions within 10 minutes of an alert are not emailed individually. A full report is retained until the UPS reaches 100%% and protected systems recover.\n", i.ID, now.Format(time.RFC3339), batteryText(up.BatteryPercent)), i.ID+fmt.Sprintf("-start-%d", len(i.Periods)))
		}
		return nil
	}
	// A confirmed ONLINE reading is required to end an outage. LastGood is ignored.
	if !e.state.SeenOnline {
		e.state.SeenOnline = true
		if err := e.persist(); err != nil {
			return err
		}
	}
	i := e.state.Current
	if i == nil {
		return nil
	}
	p := &i.Periods[len(i.Periods)-1]
	if p.EndedAt.IsZero() {
		p.EndedAt = now
		if err := e.persist(); err != nil {
			return err
		}
		e.log("email_mains_restored_recorded", "info", map[string]any{"incident": i.ID, "segment": len(i.Periods)})
	}
	if !p.EndAttempted {
		p.EndAttempted = true
		if !p.StartAttempted {
			// Brief flap inside the cooldown: no start alert was sent, so no
			// restore alert is sent either. The final report still lists it.
			p.StartAttempted = true
			p.AlertSuppressed = true
			if err := e.persist(); err != nil {
				return err
			}
			e.log("email_alert_suppressed", "info", map[string]any{"incident": i.ID, "segment": len(i.Periods)})
		} else {
			i.LastAlertAt = now
			if err := e.persist(); err != nil {
				return err
			}
			e.attempt(ctx, cfg, "APC UPS Bridge — Mains power restored", fmt.Sprintf("Incident: %s\nUPS returned to mains at: %s\nBattery: %s\n\nThe final recovery report will be sent only after 100%% charge and all monitored systems are reachable.\n", i.ID, p.EndedAt.Format(time.RFC3339), batteryText(up.BatteryPercent)), i.ID+fmt.Sprintf("-restore-%d", len(i.Periods)))
		}
	}
	// Charging at 99% is *not* complete; preserve the incident through restarts.
	if up.BatteryPercent == nil || *up.BatteryPercent < 100 || i.SummaryBlocked || now.Before(i.NextAttempt) {
		return nil
	}
	// Check devices and connectivity no more than every two minutes; never
	// create an SSH connection or SMTP attempt while the UPS is on battery.
	if !e.lastProbe.IsZero() && now.Sub(e.lastProbe) < 2*time.Minute {
		return nil
	}
	e.lastProbe = now
	if cfg.WaitForDevices && !e.hosts(ctx, i.Targets) {
		return nil
	}
	if err := e.probe(ctx, cfg); err != nil {
		if i.LastError != "SMTP gateway unreachable; report retained" {
			i.LastError = "SMTP gateway unreachable; report retained"
			_ = e.persist()
			e.log("email_summary_queued", "info", map[string]any{"incident": i.ID})
		}
		return nil
	}
	subject, body := e.summary(i, now)
	i.SummaryInFlight = true
	i.LastError = ""
	if err := e.persist(); err != nil {
		return err
	}
	err := e.sender.Send(ctx, cfg, subject, body, i.ID+"-summary")
	i.SummaryInFlight = false
	if err != nil {
		i.LastError = "SMTP delivery failed; report retained"
		i.NextAttempt = now.Add(10 * time.Minute)
		// Invalid credentials are not fixed by repeating a login every ten minutes.
		if strings.Contains(strings.ToLower(err.Error()), "authentication") {
			i.SummaryBlocked = true
			i.LastError = "SMTP authentication failed; update email settings"
		}
		if saveErr := e.persist(); saveErr != nil {
			return saveErr
		}
		e.log("email_summary_failed", "warning", map[string]any{"incident": i.ID, "reason": i.LastError})
		return nil
	}
	e.state.LastReportID = i.ID
	e.state.LastReportAt = now
	e.state.Current = nil
	if err := e.persist(); err != nil {
		return err
	}
	e.log("email_summary_sent", "info", map[string]any{"incident": i.ID})
	return nil
}
func batteryText(v *float64) string {
	if v == nil {
		return "Not reported"
	}
	return fmt.Sprintf("%.1f%%", *v)
}
func (e *Engine) attempt(ctx context.Context, c Config, subject, body, id string) {
	if err := e.sender.Send(ctx, c, subject, body, id); err != nil {
		e.log("email_alert_failed", "warning", map[string]any{"kind": subject, "reason": "SMTP delivery failed (no retry)"})
	} else {
		e.log("email_alert_sent", "info", map[string]any{"kind": subject})
	}
}

// SendSecurityWarning is best-effort and globally rate-limited to limit abuse.
func (e *Engine) SendSecurityWarning() {
	cfg := e.store.Get()
	if !cfg.Enabled {
		return
	}
	e.mu.Lock()
	if e.now().Sub(e.lastSecurityAlert) < time.Hour {
		e.mu.Unlock()
		return
	}
	e.lastSecurityAlert = e.now()
	e.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = e.sender.Send(ctx, cfg, "APC UPS Bridge — Security warning",
		"Multiple failed administrator logins suggest an attack. Check the read-only Login security page for local details. Source addresses are intentionally excluded from this email.\n",
		makeID(e.now())+"-security")
}

func (e *Engine) summary(i *Incident, now time.Time) (string, string) {
	var b strings.Builder
	fmt.Fprintf(&b, "APC UPS Bridge — Final incident report\nIncident ID: %s\nFirst detected: %s\nCompleted (UPS full / hosts verified): %s\nPower interruptions: %d\nLowest recorded battery: %s\nPi/service restarts while pending: %d\nObservation interrupted: %t\n\n", i.ID, i.CreatedAt.Format(time.RFC3339), now.Format(time.RFC3339), len(i.Periods), batteryText(i.LowestBattery), i.PiRestarts, i.ObservationInterrupted)
	for j, p := range i.Periods {
		if p.EndedAt.IsZero() {
			fmt.Fprintf(&b, "Interruption %d: open\n", j+1)
			continue
		}
		fmt.Fprintf(&b, "Interruption %d: %s -> %s (%.0f min between observations; may include host downtime)%s\n", j+1, p.StartedAt.Format(time.RFC3339), p.EndedAt.Format(time.RFC3339), p.EndedAt.Sub(p.StartedAt).Minutes(), func() string {
			if p.UncertainStart {
				return " [start uncertain]"
			}
			return ""
		}())
	}
	fmt.Fprintln(&b, "\nProtected systems (TCP SSH port reachable at completion):")
	if len(i.Targets) == 0 {
		fmt.Fprintln(&b, "No remote systems configured")
	}
	for _, target := range i.Targets {
		fmt.Fprintf(&b, "- %s\n", target.Name)
	}
	fmt.Fprintln(&b, "\nRecorded events (bounded to latest 500, not routine samples):")
	history, err := events.ReadHistory(e.logs, 0, 500, now)
	if err != nil {
		fmt.Fprintln(&b, "Event log unavailable:", err)
		return "APC UPS Bridge — Fully charged recovery report", b.String()
	}
	n := 0
	for j := len(history.Events) - 1; j >= 0; j-- {
		ev := history.Events[j]
		if ev.Time.Before(i.CreatedAt) || ev.Time.After(now) {
			continue
		}
		n++
		if n > 200 {
			fmt.Fprintln(&b, "(older events omitted)")
			break
		}
		fmt.Fprintf(&b, "%s %s", ev.Time.UTC().Format(time.RFC3339), ev.Type)
		// Preserve useful numerical metrics and configured friendly names, but
		// never include arbitrary event details: they may contain IP addresses.
		var details = map[string]any{}
		for key, value := range ev.Details {
			switch key {
			case events.DetailBatteryPercent, "load_percent", events.DetailRuntimeMinutes, "remaining_minutes", "duration_seconds", "duration_minutes", "retry", "count", "sequence", "priority":
				switch value.(type) {
				case int, int64, float64, bool:
					details[key] = value
				}
			case "name", "device_name", "friendly_name":
				if label, ok := value.(string); ok {
					for _, target := range i.Targets {
						if label == target.Name {
							details[key] = label
							break
						}
					}
				}
			}
		}
		if len(details) > 0 {
			d, _ := json.Marshal(details)
			fmt.Fprintf(&b, " %s", d)
		}
		fmt.Fprintln(&b)
	}
	return "APC UPS Bridge — Fully charged recovery report", b.String()
}
func checkHosts(ctx context.Context, ts []Target) bool {
	for _, t := range ts {
		dialer := &net.Dialer{Timeout: time.Second}
		c, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(t.Host, fmt.Sprint(t.Port)))
		if err != nil {
			return false
		}
		_ = c.Close()
	}
	return true
}

// Run ensures network I/O cannot block the UPS polling/protection loop.
func (e *Engine) Run(ctx context.Context, updates <-chan Sample, onError func(error)) {
	for {
		select {
		case <-ctx.Done():
			return
		case s, ok := <-updates:
			if !ok {
				return
			}
			if err := e.Observe(ctx, s.UPS, s.Targets); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

// SendTest sends a single explicitly requested test email; it never changes an outage.
func (e *Engine) SendTest(ctx context.Context) error {
	c := e.store.Get()
	if !c.Enabled {
		return fmt.Errorf("configure and enable SMTP first")
	}
	return e.sender.Send(ctx, c, "APC UPS Bridge — SMTP test", "SMTP notifications are configured. No power event has occurred.", makeID(e.now())+"-test")
}
