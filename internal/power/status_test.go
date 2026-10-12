package power

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProber struct {
	mu      sync.Mutex
	pingErr error
	rtt     time.Duration
	reach   bool
	pings   int
	reaches int
	cur     int32
	maxCur  int32
	delay   time.Duration
	pinged  []time.Time
}

func (f *fakeProber) enter() {
	n := atomic.AddInt32(&f.cur, 1)
	for {
		m := atomic.LoadInt32(&f.maxCur)
		if n <= m || atomic.CompareAndSwapInt32(&f.maxCur, m, n) {
			break
		}
	}
	time.Sleep(f.delay)
}
func (f *fakeProber) leave() { atomic.AddInt32(&f.cur, -1) }
func (f *fakeProber) Reachable(context.Context, Device) bool {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reaches++
	return f.reach
}
func (f *fakeProber) Ping(context.Context, string) (time.Duration, error) {
	f.enter()
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pings++
	f.pinged = append(f.pinged, time.Now())
	return f.rtt, f.pingErr
}
func (f *fakeProber) set(err error, reach bool) {
	f.mu.Lock()
	f.pingErr, f.reach = err, reach
	f.mu.Unlock()
}

type fakeConfig struct {
	mu  sync.Mutex
	cfg Config
}

func (c *fakeConfig) get() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}
func (c *fakeConfig) set(devs ...Device) {
	c.mu.Lock()
	c.cfg.Devices = devs
	c.mu.Unlock()
}

func statusDevice(id, check string) Device {
	d := testDevice()
	d.ID, d.Name, d.OnlineCheck = id, "Name "+id, check
	return d
}

func newTestMonitor(p StatusProber, devs ...Device) (*StatusMonitor, *fakeConfig) {
	c := &fakeConfig{}
	c.set(devs...)
	m := NewStatusMonitor(c.get, p)
	m.jitter = func(time.Duration) time.Duration { return 0 }
	return m, c
}

func stateOf(t *testing.T, m *StatusMonitor, id string) DeviceStatus {
	t.Helper()
	for _, s := range m.Snapshot() {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("device %s missing from snapshot", id)
	return DeviceStatus{}
}

func TestOnlineCheckConfig(t *testing.T) {
	cfg := Defaults()
	cfg.Devices = []Device{testDevice()}
	if cfg.Devices[0].OnlineCheckMethod() != OnlineCheckPing {
		t.Fatal("empty online_check must mean ping")
	}
	for _, v := range []string{"", "ping", "ssh_port", "off"} {
		cfg.Devices[0].OnlineCheck = v
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%q rejected: %v", v, err)
		}
	}
	for _, v := range []string{"PING", "tcp", "ssh", " off"} {
		cfg.Devices[0].OnlineCheck = v
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%q accepted", v)
		}
	}
}

func TestStatusTransitions(t *testing.T) {
	p := &fakeProber{rtt: 3240 * time.Microsecond}
	m, _ := newTestMonitor(p, statusDevice("a", ""))
	ctx := context.Background()
	if s := stateOf(t, m, "a"); s.State != StateUnknown || s.Method != OnlineCheckPing {
		t.Fatalf("initial %+v", s)
	}
	m.cycle(ctx)
	s := stateOf(t, m, "a")
	if s.State != StateOnline || s.RTTMS == nil || *s.RTTMS != 3.2 || s.CheckedAt == nil || s.Since == nil {
		t.Fatalf("online %+v", s)
	}
	since := *s.Since
	p.set(ErrPingTimeout, false)
	m.cycle(ctx)
	if s = stateOf(t, m, "a"); s.State != StateOnline {
		t.Fatalf("one miss must stay online: %+v", s)
	}
	m.cycle(ctx)
	if s = stateOf(t, m, "a"); s.State != StateOffline || s.RTTMS != nil {
		t.Fatalf("second miss must be offline: %+v", s)
	}
	if s.Since == nil || s.Since.Before(since) {
		t.Fatalf("since not updated: %+v", s)
	}
	p.set(nil, false)
	m.cycle(ctx)
	if s = stateOf(t, m, "a"); s.State != StateOnline {
		t.Fatalf("recovery %+v", s)
	}
	p.set(ErrPingTimeout, false)
	m.cycle(ctx)
	p.set(nil, false)
	m.cycle(ctx)
	p.set(ErrPingTimeout, false)
	m.cycle(ctx)
	if s = stateOf(t, m, "a"); s.State != StateOnline {
		t.Fatalf("misses must be consecutive: %+v", s)
	}
}

func TestStatusUnknownFailureIsOffline(t *testing.T) {
	p := &fakeProber{pingErr: ErrPingTimeout}
	m, _ := newTestMonitor(p, statusDevice("a", "ping"))
	m.cycle(context.Background())
	if s := stateOf(t, m, "a"); s.State != StateOffline {
		t.Fatalf("%+v", s)
	}
}

func TestStatusAddressFamilyUnavailable(t *testing.T) {
	p := &fakeProber{pingErr: fmt.Errorf("wrapped: %w", ErrPingAddressFamily)}
	m, _ := newTestMonitor(p, statusDevice("a", ""))
	logged := 0
	m.Logf = func(string, ...any) { logged++ }
	m.cycle(context.Background())
	s := stateOf(t, m, "a")
	if s.State != StateUnavailable || !strings.Contains(s.Message, "IPv6") || logged != 0 {
		t.Fatalf("%+v logged=%d", s, logged)
	}
}

func TestStatusUnavailable(t *testing.T) {
	p := &fakeProber{pingErr: fmt.Errorf("wrapped: %w", ErrPingNotPermitted)}
	m, _ := newTestMonitor(p, statusDevice("a", ""))
	logged := 0
	m.Logf = func(string, ...any) { logged++ }
	m.cycle(context.Background())
	m.cycle(context.Background())
	s := stateOf(t, m, "a")
	if s.State != StateUnavailable || s.Message == "" || logged != 1 {
		t.Fatalf("%+v logged=%d", s, logged)
	}
	// A name that stops resolving (e.g. a DHCP host that was switched off) is a
	// miss like any other failure, with the reason shown.
	p.set(fmt.Errorf("%w: x", ErrPingResolve), false)
	m.cycle(context.Background())
	if s = stateOf(t, m, "a"); s.State != StateOffline || s.Message != "host name could not be resolved" {
		t.Fatalf("%+v", s)
	}
}

func TestStatusProbePanicIsContained(t *testing.T) {
	m, _ := newTestMonitor(panicProber{}, statusDevice("a", ""))
	m.cycle(context.Background())
	if s := stateOf(t, m, "a"); s.State != StateOffline {
		t.Fatalf("%+v", s)
	}
}

type panicProber struct{}

func (panicProber) Reachable(context.Context, Device) bool { panic("probe failure") }
func (panicProber) Ping(context.Context, string) (time.Duration, error) {
	panic("probe failure")
}

func TestStatusMethodsAndOff(t *testing.T) {
	p := &fakeProber{reach: true}
	m, _ := newTestMonitor(p, statusDevice("off", "off"), statusDevice("ssh", "ssh_port"))
	if s := stateOf(t, m, "off"); s.State != StateOff {
		t.Fatalf("%+v", s)
	}
	m.cycle(context.Background())
	if p.pings != 0 || p.reaches != 1 {
		t.Fatalf("pings=%d reaches=%d", p.pings, p.reaches)
	}
	if s := stateOf(t, m, "ssh"); s.State != StateOnline || s.RTTMS != nil || s.Method != OnlineCheckSSHPort {
		t.Fatalf("%+v", s)
	}
	if s := stateOf(t, m, "off"); s.State != StateOff || s.CheckedAt != nil {
		t.Fatalf("%+v", s)
	}
}

func TestStatusRemovedAndChangedDevices(t *testing.T) {
	p := &fakeProber{}
	m, c := newTestMonitor(p, statusDevice("a", ""), statusDevice("b", ""))
	m.cycle(context.Background())
	if len(m.Snapshot()) != 2 || stateOf(t, m, "b").State != StateOnline {
		t.Fatal("expected two online devices")
	}
	c.set(statusDevice("b", ""))
	if snap := m.Snapshot(); len(snap) != 1 || snap[0].ID != "b" || snap[0].State != StateOnline {
		t.Fatalf("removal %+v", snap)
	}
	for name, mutate := range map[string]func(*Device){
		"host":   func(d *Device) { d.Host = "192.0.2.50" },
		"port":   func(d *Device) { d.SSHPort = 2222 },
		"method": func(d *Device) { d.OnlineCheck = "ssh_port" },
	} {
		d := statusDevice("b", "")
		mutate(&d)
		c.set(d)
		if s := stateOf(t, m, "b"); s.State != StateUnknown {
			t.Fatalf("%s change did not reset: %+v", name, s)
		}
		c.set(statusDevice("b", ""))
		m.cycle(context.Background())
	}
	// A rename alone keeps the state.
	d := statusDevice("b", "")
	d.Name = "Renamed"
	c.set(d)
	if s := stateOf(t, m, "b"); s.State != StateOnline || s.Name != "Renamed" {
		t.Fatalf("%+v", s)
	}
}

func TestStatusChecksAreSequential(t *testing.T) {
	p := &fakeProber{delay: 3 * time.Millisecond, reach: true}
	var devs []Device
	for i := 0; i < 6; i++ {
		check := "ping"
		if i%2 == 1 {
			check = "ssh_port"
		}
		devs = append(devs, statusDevice(fmt.Sprintf("d%d", i), check))
	}
	m, _ := newTestMonitor(p, devs...)
	m.cycle(context.Background())
	if p.pings+p.reaches != 6 || atomic.LoadInt32(&p.maxCur) != 1 {
		t.Fatalf("checks=%d max concurrency=%d", p.pings+p.reaches, atomic.LoadInt32(&p.maxCur))
	}
}

func TestStatusCancelledProbeIsNotRecorded(t *testing.T) {
	p := &fakeProber{pingErr: ErrPingTimeout}
	m, _ := newTestMonitor(p, statusDevice("a", ""))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.cycle(ctx)
	if s := stateOf(t, m, "a"); s.State != StateUnknown {
		t.Fatalf("%+v", s)
	}
}

func TestNudgeWait(t *testing.T) {
	m, _ := newTestMonitor(&fakeProber{})
	now := time.Now()
	if w := m.nudgeWait(now, now.Add(-time.Hour)); w != m.nudgeDelay {
		t.Fatalf("idle wait %v", w)
	}
	if w := m.nudgeWait(now, now.Add(-5*time.Second)); w != 10*time.Second {
		t.Fatalf("rate-limited wait %v", w)
	}
	if w := m.nudgeWait(now, time.Time{}); w != m.nudgeDelay {
		t.Fatalf("first wait %v", w)
	}
	// Nudge never blocks, however often it is called.
	for i := 0; i < 100; i++ {
		m.Nudge()
	}
}

func TestRunNudgeRateLimited(t *testing.T) {
	p := &fakeProber{}
	m, _ := newTestMonitor(p, statusDevice("a", ""))
	m.firstDelay, m.interval = 10*time.Millisecond, time.Hour
	m.nudgeDelay, m.nudgeMinGap = 5*time.Millisecond, 250*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	waitFor := func(n int) {
		t.Helper()
		for i := 0; i < 200; i++ {
			p.mu.Lock()
			got := p.pings
			p.mu.Unlock()
			if got >= n {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %d cycles", n)
	}
	waitFor(1)
	for i := 0; i < 10; i++ {
		m.Nudge()
	}
	waitFor(2)
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pings != 2 {
		t.Fatalf("expected exactly one extra cycle, got %d total", p.pings)
	}
	if gap := p.pinged[1].Sub(p.pinged[0]); gap < 200*time.Millisecond {
		t.Fatalf("extra cycle ran after only %v", gap)
	}
}
