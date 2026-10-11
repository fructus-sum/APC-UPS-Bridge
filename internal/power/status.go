package power

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"
)

// Online status is display-only: it never feeds shutdown or wake decisions,
// is kept in memory only, and is never written to disk or the event log.
const (
	StatusInterval     = 60 * time.Second
	statusFirstDelay   = 5 * time.Second
	statusJitterMax    = 5 * time.Second
	statusNudgeDelay   = 2 * time.Second
	statusNudgeMinGap  = 15 * time.Second
	statusPingTimeout  = 5 * time.Second // name resolution (up to 3s) plus the 2s echo wait
	statusFailuresDown = 2               // consecutive misses before an online device reads offline
)

const (
	StateUnknown     = "unknown"
	StateOnline      = "online"
	StateOffline     = "offline"
	StateUnavailable = "unavailable"
	StateOff         = "off"
)

// StatusProber is the narrow set of network checks the monitor needs.
type StatusProber interface {
	// Reachable opens and immediately closes a TCP connection; it never logs in.
	Reachable(context.Context, Device) bool
	// Ping sends a single ICMP echo request.
	Ping(ctx context.Context, host string) (time.Duration, error)
}

// DeviceStatus is the JSON view of one device's last known reachability.
type DeviceStatus struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	State     string     `json:"state"`
	Method    string     `json:"method"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	RTTMS     *float64   `json:"rtt_ms,omitempty"`
	Message   string     `json:"message,omitempty"`
}

type deviceState struct {
	host   string
	port   int
	method string
	status DeviceStatus
	misses int
}

// StatusMonitor probes each device sequentially in one goroutine.
type StatusMonitor struct {
	// Logf, if set, receives the one-time "ping not permitted" notice.
	Logf func(format string, args ...any)

	cfg    func() Config
	prober StatusProber
	now    func() time.Time
	jitter func(max time.Duration) time.Duration

	interval, firstDelay, jitterMax, nudgeDelay, nudgeMinGap time.Duration

	nudge chan struct{}

	mu        sync.Mutex
	devices   []*deviceState
	logPermit bool
}

func NewStatusMonitor(cfg func() Config, prober StatusProber) *StatusMonitor {
	return &StatusMonitor{
		cfg: cfg, prober: prober, now: time.Now,
		jitter: func(max time.Duration) time.Duration {
			if max <= 0 {
				return 0
			}
			return time.Duration(rand.Int63n(int64(max)))
		},
		interval: StatusInterval, firstDelay: statusFirstDelay, jitterMax: statusJitterMax,
		nudgeDelay: statusNudgeDelay, nudgeMinGap: statusNudgeMinGap,
		nudge: make(chan struct{}, 1),
	}
}

// Nudge asks for an extra check cycle soon, e.g. after a configuration save.
// It never blocks; Run spaces extra cycles at least nudgeMinGap apart.
func (m *StatusMonitor) Nudge() {
	select {
	case m.nudge <- struct{}{}:
	default:
	}
}

// nudgeWait is how long after a nudge the extra cycle should start, given when
// the previous cycle began.
func (m *StatusMonitor) nudgeWait(now, lastCycle time.Time) time.Duration {
	wait := m.nudgeDelay
	if gap := lastCycle.Add(m.nudgeMinGap).Sub(now); gap > wait {
		wait = gap
	}
	return wait
}

// Run checks devices until ctx is cancelled.
func (m *StatusMonitor) Run(ctx context.Context) {
	timer := time.NewTimer(m.firstDelay + m.jitter(m.jitterMax))
	defer timer.Stop()
	var last time.Time
	reset := func(d time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(d)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.nudge:
			reset(m.nudgeWait(m.now(), last))
		case <-timer.C:
			last = m.now()
			m.cycle(ctx)
			reset(m.interval + m.jitter(m.jitterMax))
		}
	}
}

func fingerprintMatches(s *deviceState, d Device) bool {
	return s.host == d.Host && s.port == d.SSHPort && s.method == d.OnlineCheckMethod()
}

// syncLocked aligns state with the configured devices: removed devices vanish
// and devices whose host, port or method changed reset to unknown.
func (m *StatusMonitor) syncLocked(devs []Device) {
	old := map[string]*deviceState{}
	for _, s := range m.devices {
		old[s.status.ID] = s
	}
	next := make([]*deviceState, 0, len(devs))
	now := m.now()
	for _, d := range devs {
		s := old[d.ID]
		if s == nil || !fingerprintMatches(s, d) {
			s = &deviceState{host: d.Host, port: d.SSHPort, method: d.OnlineCheckMethod()}
			s.status = DeviceStatus{ID: d.ID, State: StateUnknown, Method: s.method}
			if s.method == OnlineCheckOff {
				s.status.State = StateOff
				t := now
				s.status.Since = &t
			}
		}
		s.status.Name = d.Name
		next = append(next, s)
	}
	m.devices = next
}

// Snapshot returns the current status in configuration order.
func (m *StatusMonitor) Snapshot() []DeviceStatus {
	cfg := m.cfg()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncLocked(cfg.Devices)
	out := make([]DeviceStatus, len(m.devices))
	for i, s := range m.devices {
		out[i] = s.status
	}
	return out
}

func (m *StatusMonitor) cycle(ctx context.Context) {
	devs := m.cfg().Devices
	m.mu.Lock()
	m.syncLocked(devs)
	m.mu.Unlock()
	for _, d := range devs {
		if ctx.Err() != nil {
			return
		}
		if d.OnlineCheckMethod() == OnlineCheckOff {
			continue
		}
		ok, rtt, err := m.probe(ctx, d)
		if ctx.Err() != nil {
			return // a cancelled probe says nothing about the host
		}
		m.record(d, ok, rtt, err)
	}
}

// probe runs one check. Status is display-only, so a failure inside a probe is
// recorded as a miss and can never take down the bridge.
func (m *StatusMonitor) probe(ctx context.Context, d Device) (ok bool, rtt time.Duration, err error) {
	defer func() {
		if r := recover(); r != nil {
			ok, rtt, err = false, 0, fmt.Errorf("online check failed: %v", r)
		}
	}()
	switch d.OnlineCheckMethod() {
	case OnlineCheckPing:
		cctx, cancel := context.WithTimeout(ctx, statusPingTimeout)
		defer cancel()
		rtt, err = m.prober.Ping(cctx, d.Host)
		return err == nil, rtt, err
	case OnlineCheckSSHPort:
		return m.prober.Reachable(ctx, d), 0, nil
	}
	return false, 0, nil
}

func (m *StatusMonitor) record(d Device, ok bool, rtt time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var s *deviceState
	for _, c := range m.devices {
		if c.status.ID == d.ID && fingerprintMatches(c, d) {
			s = c
		}
	}
	if s == nil {
		return // removed or reconfigured while the probe ran
	}
	now := m.now()
	prev := s.status.State
	st := &s.status
	st.CheckedAt = &now
	st.RTTMS, st.Message = nil, ""
	switch {
	case ok:
		s.misses = 0
		st.State = StateOnline
		if s.method == OnlineCheckPing {
			ms := math.Round(float64(rtt)/float64(time.Millisecond)*10) / 10
			st.RTTMS = &ms
		}
	case errors.Is(err, ErrPingNotPermitted):
		s.misses = 0
		st.State = StateUnavailable
		st.Message = "ping is not permitted for the bridge service; choose SSH port or Off"
		if !m.logPermit && m.Logf != nil {
			m.logPermit = true
			m.Logf("device online checks: ping is not permitted for this service (see net.ipv4.ping_group_range); affected devices show as unavailable (repeats suppressed)")
		}
	default:
		// A name that no longer resolves (common for DHCP hosts that are
		// switched off) counts as a miss, with the reason shown.
		if errors.Is(err, ErrPingResolve) {
			st.Message = "host name could not be resolved"
		}
		s.misses++
		if prev == StateOnline && s.misses < statusFailuresDown {
			st.State = StateOnline
		} else {
			st.State = StateOffline
		}
	}
	if st.State != prev || st.Since == nil {
		t := now
		st.Since = &t
	}
}
