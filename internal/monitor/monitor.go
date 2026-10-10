// Package monitor maintains the latest UPS snapshot and significant changes.
// It intentionally executes no remote shutdown or wake-up actions.
package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
)

// Status is safe to return to future dashboards. A failed poll is not reported
// as a healthy, fresh reading; LastGood remains explicitly marked as historic.
type Status struct {
	Healthy   bool          `json:"healthy"`
	CheckedAt time.Time     `json:"checked_at"`
	Error     string        `json:"error,omitempty"`
	Current   *apc.Snapshot `json:"current,omitempty"`
	LastGood  *apc.Snapshot `json:"last_good,omitempty"`
}

type Monitor struct {
	mu       sync.RWMutex
	reader   apc.Reader
	writer   events.Writer
	lowAt    float64
	status   Status
	prior    apc.PowerState
	hasPrior bool
	bad      bool
	gap      bool
	low      bool
	outageAt time.Time
	lowest   *float64
	now      func() time.Time
}

func New(reader apc.Reader, writer events.Writer, lowBatteryThreshold float64) (*Monitor, error) {
	if reader == nil || writer == nil {
		return nil, fmt.Errorf("reader and writer are required")
	}
	if lowBatteryThreshold < 0 || lowBatteryThreshold > 100 {
		return nil, fmt.Errorf("low battery threshold must be 0..100")
	}
	return &Monitor{reader: reader, writer: writer, lowAt: lowBatteryThreshold, now: time.Now}, nil
}

// Snapshot returns a copy of the current monitor state.
func (m *Monitor) Snapshot() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneStatus(m.status)
}

func copyReading(s *apc.Snapshot) *apc.Snapshot {
	v := *s
	v.Flags = append([]string(nil), s.Flags...)
	copyFloat := func(f *float64) *float64 {
		if f == nil {
			return nil
		}
		x := *f
		return &x
	}
	v.BatteryPercent = copyFloat(v.BatteryPercent)
	v.RuntimeMinutes = copyFloat(v.RuntimeMinutes)
	v.LoadPercent = copyFloat(v.LoadPercent)
	v.InputVolts = copyFloat(v.InputVolts)
	v.OutputVolts = copyFloat(v.OutputVolts)
	return &v
}

func cloneStatus(s Status) Status {
	if s.Current != nil {
		s.Current = copyReading(s.Current)
	}
	if s.LastGood != nil {
		s.LastGood = copyReading(s.LastGood)
	}
	return s
}

// Poll samples once, updates in-memory state, and writes only significant
// transitions. It returns errors from hardware or event logging to its caller.
// Poll must not be invoked concurrently.
func (m *Monitor) Poll(ctx context.Context) error {
	snap, readErr := m.reader.Read(ctx)
	now := m.now().UTC()
	if readErr == nil && snap.Power == apc.Unknown {
		readErr = fmt.Errorf("ambiguous or unavailable UPS status: %q", snap.Status)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.CheckedAt = now
	if readErr != nil {
		m.status.Healthy = false
		m.status.Current = nil
		m.status.Error = readErr.Error()
		m.gap = true
		if !m.bad {
			if err := m.emit(now, "ups_communication_lost", "warning", map[string]any{"reason": readErr.Error()}); err != nil {
				return fmt.Errorf("%v; %w", readErr, err)
			}
			m.bad = true
		}
		return readErr
	}
	snap.ObservedAt = now
	m.status.Healthy = true
	m.status.Error = ""
	m.status.Current = &snap
	m.status.LastGood = &snap
	if m.bad {
		if err := m.emit(now, "ups_communication_restored", "info", nil); err != nil {
			return err
		}
		m.bad = false
	}
	if snap.Power == apc.OnBattery {
		if !m.hasPrior || m.prior != apc.OnBattery {
			eventType := "power_lost"
			if !m.hasPrior || m.prior != apc.Online || m.gap {
				eventType = "on_battery_detected"
			}
			m.outageAt = now
			m.lowest = nil
			m.low = false
			if err := m.emit(now, eventType, "warning", metricDetails(snap)); err != nil {
				return err
			}
		}
		if snap.BatteryPercent != nil {
			if m.lowest == nil || *snap.BatteryPercent < *m.lowest {
				v := *snap.BatteryPercent
				m.lowest = &v
			}
		}
		isLow := (snap.BatteryPercent != nil && *snap.BatteryPercent <= m.lowAt) || hasFlag(snap.Flags, "LOWBATT")
		if isLow && !m.low {
			if err := m.emit(now, "low_battery", "warning", metricDetails(snap)); err != nil {
				return err
			}
		}
		m.low = isLow
	} else if m.hasPrior && m.prior == apc.OnBattery {
		details := metricDetails(snap)
		if !m.outageAt.IsZero() {
			details["observed_outage_seconds"] = int(now.Sub(m.outageAt).Seconds())
		}
		if m.lowest != nil {
			details["lowest_observed_battery_percent"] = *m.lowest
		}
		if m.gap {
			details["observation_interrupted"] = true
		}
		if err := m.emit(now, "power_restored", "info", details); err != nil {
			return err
		}
		m.outageAt = time.Time{}
		m.lowest = nil
		m.low = false
	}
	m.prior = snap.Power
	m.hasPrior = true
	m.gap = false
	return nil
}

func (m *Monitor) emit(t time.Time, typ, level string, details map[string]any) error {
	return m.writer.Write(events.Event{Time: t, Type: typ, Level: level, Details: details})
}
func metricDetails(s apc.Snapshot) map[string]any {
	d := map[string]any{"status": s.Status}
	if s.BatteryPercent != nil {
		d["battery_percent"] = *s.BatteryPercent
	}
	if s.RuntimeMinutes != nil {
		d["runtime_minutes"] = *s.RuntimeMinutes
	}
	return d
}
func hasFlag(flags []string, name string) bool {
	for _, flag := range flags {
		if flag == name {
			return true
		}
	}
	return false
}

// SetLowBatteryThreshold changes the event warning threshold without restarting.
// It does not enable or perform any power-management actions.
func (m *Monitor) SetLowBatteryThreshold(value float64) error {
	if value < 0 || value > 100 {
		return fmt.Errorf("low battery threshold must be 0..100")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lowAt = value
	return nil
}
