package power

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
)

// fakeExecutor is safe for concurrent use: shutdowns of one priority group run
// in parallel. Tests read the recorded slices only between Observe calls.
type fakeExecutor struct {
	mu           sync.Mutex
	shutdowns    []string
	wakes        []string
	checks       []string
	failShutdown bool
	failWake     bool
	up           bool // what Reachable reports; the zero value means hosts are down
	// onShutdown, when set, runs inside every Shutdown call (no fake lock held)
	// and can block to simulate slow SSH.
	onShutdown func(Device)
	// onReachable behaves like onShutdown for liveness checks.
	onReachable func(Device)
}

func (f *fakeExecutor) Shutdown(_ context.Context, d Device) error {
	f.mu.Lock()
	f.shutdowns = append(f.shutdowns, d.ID)
	fail := f.failShutdown
	hook := f.onShutdown
	f.mu.Unlock()
	if hook != nil {
		hook(d)
	}
	if fail {
		return errors.New("ssh failed")
	}
	return nil
}
func (f *fakeExecutor) Wake(_ context.Context, d Device) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wakes = append(f.wakes, d.ID)
	if f.failWake {
		return errors.New("udp failed")
	}
	return nil
}
func (f *fakeExecutor) Reachable(_ context.Context, d Device) bool {
	f.mu.Lock()
	f.checks = append(f.checks, d.ID)
	up := f.up
	hook := f.onReachable
	f.mu.Unlock()
	if hook != nil {
		hook(d)
	}
	return up
}

type memoryWriter struct {
	events []events.Event
	fail   bool
}

func (m *memoryWriter) Write(e events.Event) error {
	if m.fail {
		return errors.New("log unavailable")
	}
	m.events = append(m.events, e)
	return nil
}
func sample(power apc.PowerState, battery float64) monitor.Status {
	return monitor.Status{Healthy: true, Current: &apc.Snapshot{Power: power, BatteryPercent: &battery}}
}
func badSample() monitor.Status { return monitor.Status{Healthy: false} }
func testDevice() Device {
	return Device{ID: "node-1", Name: "Node one", Host: "10.20.30.11", SSHUser: "root", SSHPort: 22, MAC: "00:d0:b4:02:4c:70", WakeAddress: "192.168.1.255", WakePort: 9, ShutdownEnabled: true, WakeEnabled: true}
}
func configured(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenConfig(filepath.Join(dir, "power.json"))
	if err != nil {
		t.Fatal(err)
	}
	v := Defaults()
	v.Enabled = true
	v.Devices = []Device{testDevice()}
	v.OnBatteryDelaySeconds = 10
	v.RecoveryStableSeconds = 30
	if err = s.Save(v); err != nil {
		t.Fatal(err)
	}
	return s, filepath.Join(dir, "state.json")
}
func makeController(t *testing.T, s *Store, path string, w *memoryWriter, f *fakeExecutor, clock *time.Time) *Controller {
	t.Helper()
	c, err := OpenController(s, path, w, f)
	if err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return *clock }
	return c
}
func observe(t *testing.T, c *Controller, s monitor.Status) {
	t.Helper()
	if err := c.Observe(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownOnceRecoveryAndRestart(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	// At the initial online poll, the controller becomes armed and writes state.
	observe(t, c, sample(apc.Online, 100))
	now = now.Add(2 * time.Second)
	observe(t, c, sample(apc.OnBattery, 80))
	now = now.Add(12 * time.Second)
	observe(t, c, sample(apc.OnBattery, 30))
	if len(f.shutdowns) != 0 {
		t.Fatal("premature shutdown")
	}
	observe(t, c, sample(apc.OnBattery, 19))
	observe(t, c, sample(apc.OnBattery, 14))
	if len(f.shutdowns) != 1 {
		t.Fatalf("shutdown count %d", len(f.shutdowns))
	}
	// Restart in the same outage must not repeat the shutdown.
	c = makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.OnBattery, 12))
	if len(f.shutdowns) != 1 {
		t.Fatal("repeated shutdown after restart")
	}
	now = now.Add(time.Second)
	observe(t, c, sample(apc.Online, 35))
	now = now.Add(60 * time.Second)
	observe(t, c, sample(apc.Online, 38))
	if len(f.wakes) != 0 {
		t.Fatal("unsafe recovery with insufficient charge")
	}
	now = now.Add(time.Minute)
	observe(t, c, badSample()) // communication loss resets stability
	observe(t, c, sample(apc.Online, 60))
	if len(f.wakes) != 0 {
		t.Fatal("recovery bypassed communication confirmation")
	}
	now = now.Add(31 * time.Second)
	observe(t, c, sample(apc.Online, 60))
	if len(f.wakes) != 1 {
		t.Fatalf("wake count %d", len(f.wakes))
	}
	c = makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.Online, 60))
	if len(f.wakes) != 1 {
		t.Fatal("repeated wake after restart")
	}
}
func TestBootingIntoOutageWillNotShutdown(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Now().UTC()
	c := makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.OnBattery, 5))
	now = now.Add(time.Minute)
	observe(t, c, sample(apc.OnBattery, 5))
	if len(f.shutdowns) != 0 {
		t.Fatal("unarmed shutdown")
	}
	observe(t, c, sample(apc.Online, 80))
	now = now.Add(time.Second)
	observe(t, c, sample(apc.OnBattery, 19))
	now = now.Add(11 * time.Second)
	observe(t, c, sample(apc.OnBattery, 19))
	if len(f.shutdowns) != 1 {
		t.Fatal("should act after previously observing a healthy online state")
	}
}
func TestFailureNoEarlyRetryOrWake(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{failShutdown: true}
	now := time.Now().UTC()
	c := makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.Online, 100))
	observe(t, c, sample(apc.OnBattery, 20))
	now = now.Add(11 * time.Second)
	observe(t, c, sample(apc.OnBattery, 19))
	c = makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.OnBattery, 10))
	if len(f.shutdowns) != 1 {
		t.Fatal("failed command retried before the retry delay")
	}
	observe(t, c, sample(apc.Online, 80))
	now = now.Add(31 * time.Second)
	observe(t, c, sample(apc.Online, 80))
	if len(f.wakes) != 0 {
		t.Fatal("woke host without accepted shutdown")
	}
}
func TestDisarmingAndConfigurationLock(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Now().UTC()
	c := makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.Online, 90))
	observe(t, c, sample(apc.OnBattery, 30))
	cfg := store.Get()
	cfg.ShutdownBatteryPercent = 30
	if err := c.UpdateConfig(cfg); err == nil {
		t.Fatal("active outage accepted rule edit")
	}
	cfg.Enabled = false
	if err := c.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	observe(t, c, sample(apc.OnBattery, 5))
	if len(f.shutdowns) != 0 {
		t.Fatal("acted when disabled")
	}
	if c.View().Active || c.View().Armed {
		t.Fatal("did not disarm")
	}
}
func TestRecoveryInterruptedByNewBattery(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Now().UTC()
	c := makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.Online, 100))
	observe(t, c, sample(apc.OnBattery, 100))
	now = now.Add(11 * time.Second)
	observe(t, c, sample(apc.OnBattery, 15))
	observe(t, c, sample(apc.Online, 99))
	now = now.Add(20 * time.Second)
	observe(t, c, sample(apc.OnBattery, 10))
	now = now.Add(12 * time.Second)
	observe(t, c, sample(apc.Online, 100))
	if len(f.wakes) != 0 {
		t.Fatal("woke during power instability")
	}
	now = now.Add(31 * time.Second)
	observe(t, c, sample(apc.Online, 100))
	if len(f.wakes) != 1 || len(f.shutdowns) != 1 {
		t.Fatal("did not correctly resume original outage")
	}
}
func TestUnsafeStateAndStorageFailureBlockActions(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Now().UTC()
	if err := os.WriteFile(path, []byte(`{invalid`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenController(store, path, w, f); err == nil {
		t.Fatal("corrupt state accepted")
	}
	_ = os.Remove(path)
	c := makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.Online, 100))
	observe(t, c, sample(apc.OnBattery, 100))
	now = now.Add(11 * time.Second)
	w.fail = true
	if err := c.Observe(context.Background(), sample(apc.OnBattery, 15)); err == nil {
		t.Fatal("log failure hidden")
	}
	if len(f.shutdowns) != 1 {
		t.Fatal("event log failure must not prevent the shutdown")
	}
	w.fail = false
	observe(t, c, sample(apc.OnBattery, 15))
	if len(f.shutdowns) != 1 {
		t.Fatal("repeated shutdown of an accepted host")
	}
}
func TestConfigValidation(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Enabled = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("armed without devices")
	}
	d := testDevice()
	cfg.Devices = []Device{d}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	d.SSHUser = "-oProxyCommand=x"
	cfg.Devices = []Device{d}
	if err := cfg.Validate(); err == nil {
		t.Fatal("SSH option injection accepted")
	}
	d = testDevice()
	d.Host = "-bad"
	cfg.Devices = []Device{d}
	if err := cfg.Validate(); err == nil {
		t.Fatal("host injection accepted")
	}
	d = testDevice()
	d.MAC = "not-a-mac"
	cfg.Devices = []Device{d}
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid MAC accepted")
	}
}
func TestMagicPacket(t *testing.T) {
	packet, err := MagicPacket("00:d0:b4:02:4c:70")
	if err != nil || len(packet) != 102 {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if packet[i] != 255 {
			t.Fatal("missing preamble")
		}
	}
	for i := 0; i < 16; i++ {
		if packet[6+i*6] != 0 || packet[6+i*6+1] != 0xd0 {
			t.Fatal("invalid repeated mac")
		}
	}
}

func TestPriorityAndRuntimeFallback(t *testing.T) {
	store, path := configured(t)
	cfg := store.Get()
	first := testDevice()
	first.ID = "low-priority"
	first.Name = "Third server"
	first.Priority = 90
	first.Host = "10.20.30.13"
	second := testDevice()
	second.ID = "high-priority"
	second.Name = "First server"
	second.Priority = 2
	second.Host = "10.20.30.12"
	cfg.Devices = []Device{first, second}
	cfg.ShutdownRuntimeMinutes = 4
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	w := &memoryWriter{}
	f := &fakeExecutor{}
	c := makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.Online, 80))
	observe(t, c, sample(apc.OnBattery, 80))
	now = now.Add(11 * time.Second)
	runtime := 3.5
	s := sample(apc.OnBattery, 80)
	s.Current.RuntimeMinutes = &runtime
	observe(t, c, s)
	if len(f.shutdowns) != 2 || f.shutdowns[0] != "high-priority" || f.shutdowns[1] != "low-priority" {
		t.Fatalf("wrong priority order: %v", f.shutdowns)
	}
}
func TestWakeFailureIsNotRepeated(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{failWake: true}
	now := time.Now().UTC()
	c := makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.Online, 100))
	observe(t, c, sample(apc.OnBattery, 100))
	now = now.Add(11 * time.Second)
	observe(t, c, sample(apc.OnBattery, 10))
	observe(t, c, sample(apc.Online, 100))
	now = now.Add(31 * time.Second)
	observe(t, c, sample(apc.Online, 100))
	c = makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.Online, 100))
	if len(f.wakes) != 1 {
		t.Fatal("failed wake automatically repeated")
	}
}
func TestMissingBatteryDoesNotWake(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Now().UTC()
	c := makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.Online, 100))
	observe(t, c, sample(apc.OnBattery, 100))
	now = now.Add(11 * time.Second)
	observe(t, c, sample(apc.OnBattery, 10))
	s := sample(apc.Online, 100)
	s.Current.BatteryPercent = nil
	observe(t, c, s)
	now = now.Add(10 * time.Minute)
	observe(t, c, s)
	if len(f.wakes) != 0 {
		t.Fatal("woke with missing battery charge")
	}
}

func countEvents(w *memoryWriter, typ string) int {
	n := 0
	for _, e := range w.events {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// triggerShutdown arms the controller, starts an outage and delivers the first
// low-battery reading, which makes the first shutdown attempt.
func triggerShutdown(t *testing.T, c *Controller, now *time.Time) {
	t.Helper()
	observe(t, c, sample(apc.Online, 100))
	observe(t, c, sample(apc.OnBattery, 100))
	*now = now.Add(11 * time.Second)
	observe(t, c, sample(apc.OnBattery, 15))
}

func TestShutdownRetrySucceedsOnSecondAttempt(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{failShutdown: true}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	triggerShutdown(t, c, &now)
	if len(f.shutdowns) != 1 {
		t.Fatalf("first attempt count %d", len(f.shutdowns))
	}
	f.failShutdown = false
	now = now.Add(30 * time.Second)
	observe(t, c, sample(apc.OnBattery, 15))
	if len(f.shutdowns) != 2 {
		t.Fatalf("retry count %d", len(f.shutdowns))
	}
	a := c.View().Actions["node-1"]
	if !a.ShutdownAccepted || a.ShutdownAttempts != 2 {
		t.Fatalf("unexpected action state %+v", a)
	}
	// An accepted shutdown is never repeated.
	now = now.Add(5 * time.Minute)
	observe(t, c, sample(apc.OnBattery, 10))
	if len(f.shutdowns) != 2 {
		t.Fatal("accepted shutdown repeated")
	}
	if countEvents(w, "remote_shutdown_failed") != 1 || countEvents(w, "remote_shutdown_accepted") != 1 {
		t.Fatal("missing shutdown result events")
	}
	// The retried host still qualifies for automatic wake after recovery.
	observe(t, c, sample(apc.Online, 100))
	now = now.Add(31 * time.Second)
	observe(t, c, sample(apc.Online, 100))
	if len(f.wakes) != 1 {
		t.Fatalf("wake count %d", len(f.wakes))
	}
}

func TestNoShutdownRetryBeforeDelay(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{failShutdown: true}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	triggerShutdown(t, c, &now)
	now = now.Add(29 * time.Second)
	observe(t, c, sample(apc.OnBattery, 15))
	if len(f.shutdowns) != 1 {
		t.Fatalf("retried before delay: %d", len(f.shutdowns))
	}
	now = now.Add(time.Second)
	observe(t, c, sample(apc.OnBattery, 15))
	if len(f.shutdowns) != 2 {
		t.Fatalf("no retry after delay: %d", len(f.shutdowns))
	}
}

func TestShutdownGivesUpAfterMaxAttempts(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{failShutdown: true}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	triggerShutdown(t, c, &now)
	for i := 0; i < 12; i++ {
		now = now.Add(shutdownRetryDelay)
		observe(t, c, sample(apc.OnBattery, 15))
	}
	if len(f.shutdowns) != maxShutdownAttempts {
		t.Fatalf("attempt count %d, want %d", len(f.shutdowns), maxShutdownAttempts)
	}
	if got := countEvents(w, "remote_shutdown_failed"); got != maxShutdownAttempts {
		t.Fatalf("failed events %d", got)
	}
	if got := countEvents(w, "remote_shutdown_gave_up"); got != 1 {
		t.Fatalf("gave up events %d", got)
	}
	// A restart does not reset the budget.
	c = makeController(t, store, path, w, f, &now)
	now = now.Add(time.Hour)
	observe(t, c, sample(apc.OnBattery, 15))
	if len(f.shutdowns) != maxShutdownAttempts {
		t.Fatal("retried beyond the attempt limit after restart")
	}
	// A host that never accepted shutdown is never woken.
	observe(t, c, sample(apc.Online, 100))
	now = now.Add(31 * time.Second)
	observe(t, c, sample(apc.Online, 100))
	if len(f.wakes) != 0 {
		t.Fatal("woke a host that never accepted shutdown")
	}
}

func TestLegacyShutdownAttemptCountsAsOne(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{failShutdown: true}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	observe(t, c, sample(apc.Online, 100))
	observe(t, c, sample(apc.OnBattery, 100))
	// State written before attempt counting existed: attempted, but no counter.
	c.state.Actions["node-1"] = ActionState{ShutdownAttempted: true}
	if err := c.persist(); err != nil {
		t.Fatal(err)
	}
	c = makeController(t, store, path, w, f, &now)
	now = now.Add(11 * time.Second)
	for i := 0; i < 12; i++ {
		observe(t, c, sample(apc.OnBattery, 15))
		now = now.Add(shutdownRetryDelay)
	}
	if want := maxShutdownAttempts - 1; len(f.shutdowns) != want {
		t.Fatalf("attempt count %d, want %d", len(f.shutdowns), want)
	}
	if a := c.View().Actions["node-1"]; a.ShutdownAttempts != maxShutdownAttempts {
		t.Fatalf("unexpected action state %+v", a)
	}
}

func TestShutdownRetryContinuesAfterBatteryFlicker(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{failShutdown: true}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	triggerShutdown(t, c, &now)
	f.failShutdown = false
	now = now.Add(30 * time.Second)
	// The reading rose above the threshold, but the shutdown condition was
	// already met in this outage, so the failed host must still be retried.
	observe(t, c, sample(apc.OnBattery, 60))
	if len(f.shutdowns) != 2 {
		t.Fatalf("retry skipped after battery flicker: %d", len(f.shutdowns))
	}
	if !c.View().Actions["node-1"].ShutdownAccepted {
		t.Fatal("retry not recorded as accepted")
	}
}

func TestEventWriterFailureDoesNotBlockActions(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{fail: true}
	f := &fakeExecutor{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	if err := c.Observe(context.Background(), sample(apc.Online, 100)); err != nil {
		t.Fatal(err)
	}
	if err := c.Observe(context.Background(), sample(apc.OnBattery, 100)); err == nil {
		t.Fatal("event failure hidden")
	}
	if !c.View().Active {
		t.Fatal("event failure prevented the cycle from starting")
	}
	now = now.Add(11 * time.Second)
	if err := c.Observe(context.Background(), sample(apc.OnBattery, 15)); err == nil {
		t.Fatal("event failure hidden")
	}
	if len(f.shutdowns) != 1 || !c.View().Actions["node-1"].ShutdownAccepted {
		t.Fatal("event failure prevented shutdown")
	}
	if err := c.Observe(context.Background(), sample(apc.Online, 100)); err == nil {
		t.Fatal("event failure hidden")
	}
	now = now.Add(31 * time.Second)
	if err := c.Observe(context.Background(), sample(apc.Online, 100)); err == nil {
		t.Fatal("event failure hidden")
	}
	if len(f.wakes) != 1 {
		t.Fatal("event failure prevented wake")
	}
	if c.View().Active {
		t.Fatal("event failure prevented cycle completion")
	}
}

// shutDownThenRecover shuts the host down, delivers the first mains sample
// (which starts the recovery confirmation) and moves past the stable period.
func shutDownThenRecover(t *testing.T, c *Controller, now *time.Time, first monitor.Status) {
	t.Helper()
	triggerShutdown(t, c, now)
	observe(t, c, first)
	*now = now.Add(31 * time.Second)
}

func TestRecoveryCompletesAfterTimeoutWithoutBattery(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	noBattery := sample(apc.Online, 100)
	noBattery.Current.BatteryPercent = nil
	shutDownThenRecover(t, c, &now, noBattery)
	observe(t, c, noBattery)
	now = now.Add(58 * time.Minute)
	observe(t, c, noBattery)
	if len(f.wakes) != 0 || !c.View().Active {
		t.Fatal("recovered before the battery timeout")
	}
	// 31 s + 58 min + 2 min after the recovery started: past one hour.
	now = now.Add(2 * time.Minute)
	observe(t, c, noBattery)
	if len(f.wakes) != 1 || c.View().Active {
		t.Fatalf("recovery did not complete: wakes=%d active=%v", len(f.wakes), c.View().Active)
	}
	if countEvents(w, "power_recovery_battery_timeout") != 1 {
		t.Fatal("missing battery timeout event")
	}
}

func TestRecoveryCompletesAfterTimeoutBelowThreshold(t *testing.T) {
	store, path := configured(t)
	cfg := store.Get()
	cfg.RecoveryBatteryPercent = 100
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	shutDownThenRecover(t, c, &now, sample(apc.Online, 99))
	observe(t, c, sample(apc.Online, 99))
	now = now.Add(30 * time.Minute)
	observe(t, c, sample(apc.Online, 99))
	if len(f.wakes) != 0 {
		t.Fatal("recovered at 99% before the battery timeout")
	}
	now = now.Add(31 * time.Minute)
	observe(t, c, sample(apc.Online, 99))
	if len(f.wakes) != 1 || c.View().Active {
		t.Fatalf("recovery did not complete: wakes=%d active=%v", len(f.wakes), c.View().Active)
	}
	if countEvents(w, "power_recovery_battery_timeout") != 1 {
		t.Fatal("missing battery timeout event")
	}
}

func TestLowBatteryFlagBlocksRecoveryTimeout(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	low := sample(apc.Online, 100)
	low.Current.Flags = []string{"LOWBATT"}
	shutDownThenRecover(t, c, &now, low)
	now = now.Add(3 * time.Hour)
	observe(t, c, low)
	if len(f.wakes) != 0 || !c.View().Active {
		t.Fatal("LOWBATT did not block recovery")
	}
	// Once the flag clears, recovery proceeds normally.
	observe(t, c, sample(apc.Online, 100))
	if len(f.wakes) != 1 {
		t.Fatal("recovery did not proceed after LOWBATT cleared")
	}
}

func TestCancelledContextDoesNothing(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	stateBytes := func() []byte {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	// Before any shutdown: a stale low-battery snapshot must not burn an attempt.
	observe(t, c, sample(apc.Online, 100))
	observe(t, c, sample(apc.OnBattery, 100))
	now = now.Add(11 * time.Second)
	before := stateBytes()
	eventsBefore := len(w.events)
	if err := c.Observe(cancelled, sample(apc.OnBattery, 15)); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context error, got %v", err)
	}
	if len(f.shutdowns) != 0 || string(stateBytes()) != string(before) || len(w.events) != eventsBefore {
		t.Fatal("cancelled context changed state or ran a shutdown")
	}
	observe(t, c, sample(apc.OnBattery, 15))
	if len(f.shutdowns) != 1 || c.View().Actions["node-1"].ShutdownAttempts != 1 {
		t.Fatal("attempt budget was spent by the cancelled observation")
	}

	// During recovery: a cancelled context must not spend the one-shot wake.
	observe(t, c, sample(apc.Online, 100))
	now = now.Add(31 * time.Second)
	before = stateBytes()
	eventsBefore = len(w.events)
	if err := c.Observe(cancelled, sample(apc.Online, 100)); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context error, got %v", err)
	}
	if len(f.wakes) != 0 || !c.View().Active || string(stateBytes()) != string(before) || len(w.events) != eventsBefore {
		t.Fatal("cancelled context changed state or sent a wake")
	}
	if c.View().Actions["node-1"].WakeAttempted {
		t.Fatal("wake attempt consumed by cancelled observation")
	}
	observe(t, c, sample(apc.Online, 100))
	if len(f.wakes) != 1 || c.View().Active {
		t.Fatal("recovery did not complete after the cancelled observation")
	}
}

func (f *fakeExecutor) shutdownCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.shutdowns...)
}

func deviceWith(id string, priority int, host string) Device {
	d := testDevice()
	d.ID, d.Name, d.Priority, d.Host = id, id, priority, host
	return d
}

func setDevices(t *testing.T, store *Store, devices ...Device) {
	t.Helper()
	cfg := store.Get()
	cfg.Devices = devices
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

// armedOutage arms the controller and starts an outage, leaving it ready for
// the first low-battery reading.
func armedOutage(t *testing.T, c *Controller, now *time.Time) {
	t.Helper()
	observe(t, c, sample(apc.Online, 100))
	observe(t, c, sample(apc.OnBattery, 100))
	*now = now.Add(11 * time.Second)
}

// observeAsync runs Observe in the background and returns a channel with its result.
func observeAsync(c *Controller, s monitor.Status) chan error {
	done := make(chan error, 1)
	go func() { done <- c.Observe(context.Background(), s) }()
	return done
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Observe did not return")
		return nil
	}
}

func TestSamePriorityShutdownsRunConcurrently(t *testing.T) {
	store, path := configured(t)
	setDevices(t, store, deviceWith("a", 5, "10.20.30.21"), deviceWith("b", 5, "10.20.30.22"))
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	armedOutage(t, c, &now)

	// Neither call returns until both have started, so a sequential
	// implementation can never get past the first one.
	var started sync.WaitGroup
	started.Add(2)
	release := make(chan struct{})
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	f.onShutdown = func(Device) {
		started.Done()
		<-release
	}
	done := observeAsync(c, sample(apc.OnBattery, 15))
	both := make(chan struct{})
	go func() { started.Wait(); close(both) }()
	waitFor(t, both, "both same-priority shutdowns to start")
	releaseAll()
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if !c.View().Actions[id].ShutdownAccepted {
			t.Fatalf("%s not recorded as accepted", id)
		}
	}
}

func TestLowerPriorityWaitsForHigherPriorityGroup(t *testing.T) {
	store, path := configured(t)
	setDevices(t, store, deviceWith("hi-a", 1, "10.20.30.21"), deviceWith("hi-b", 1, "10.20.30.22"), deviceWith("lo", 9, "10.20.30.23"))
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	armedOutage(t, c, &now)

	var started sync.WaitGroup
	started.Add(2)
	release := make(chan struct{})
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	f.onShutdown = func(d Device) {
		if d.ID == "lo" {
			return
		}
		started.Done()
		<-release
	}
	done := observeAsync(c, sample(apc.OnBattery, 15))
	both := make(chan struct{})
	go func() { started.Wait(); close(both) }()
	waitFor(t, both, "the high-priority group to start")
	// Give a wrongly eager implementation time to start the lower group.
	time.Sleep(100 * time.Millisecond)
	if got := f.shutdownCalls(); len(got) != 2 {
		t.Fatalf("lower priority dispatched before the higher group returned: %v", got)
	}
	releaseAll()
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	got := f.shutdownCalls()
	if len(got) != 3 || got[2] != "lo" {
		t.Fatalf("unexpected dispatch order %v", got)
	}
}

func TestViewAndUpdateConfigDoNotWaitForShutdownIO(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	armedOutage(t, c, &now)

	inCall := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	f.onShutdown = func(Device) {
		close(inCall)
		<-release
	}
	done := observeAsync(c, sample(apc.OnBattery, 15))
	waitFor(t, inCall, "the shutdown call to start")

	viewed := make(chan struct{})
	go func() {
		v := c.View()
		if !v.Active || !v.Actions["node-1"].ShutdownAttempted {
			t.Error("View did not show the persisted intent")
		}
		cfg := store.Get()
		cfg.Enabled = false
		if err := c.UpdateConfig(cfg); err != nil {
			t.Error(err)
		}
		close(viewed)
	}()
	waitFor(t, viewed, "View and UpdateConfig while a shutdown call is blocked")
	releaseAll()
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	// Automation was disabled mid-flight: the next sample clears the cycle and
	// no further command is sent.
	now = now.Add(10 * time.Minute)
	observe(t, c, sample(apc.OnBattery, 10))
	if v := c.View(); v.Active || v.Armed || len(v.Actions) != 0 {
		t.Fatalf("disabled automation left state behind: %+v", v)
	}
	if len(f.shutdownCalls()) != 1 {
		t.Fatal("command sent after automation was disabled")
	}
}

func TestResultsAreDroppedWhenCycleWasClearedDuringIO(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	armedOutage(t, c, &now)
	// Simulate the cycle being disarmed while the command is in flight. The hook
	// takes c.mu, which would deadlock if Observe held it across executor I/O.
	f.onShutdown = func(Device) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.state = State{Actions: map[string]ActionState{}}
		_ = c.persist()
	}
	observe(t, c, sample(apc.OnBattery, 15))
	if v := c.View(); v.Active || len(v.Actions) != 0 {
		t.Fatalf("in-flight result resurrected cleared state: %+v", v)
	}
	if countEvents(w, "remote_shutdown_accepted") != 0 {
		t.Fatal("result of a discarded cycle was reported")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "node-1") {
		t.Fatalf("persisted state resurrected: %s", b)
	}
}

func TestAcceptedHostStillReachableIsSentAgain(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{up: true}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	triggerShutdown(t, c, &now)
	now = now.Add(shutdownVerifyDelay - time.Second)
	observe(t, c, sample(apc.OnBattery, 15))
	if len(f.checks) != 0 || len(f.shutdowns) != 1 {
		t.Fatalf("verified too early: checks=%d shutdowns=%d", len(f.checks), len(f.shutdowns))
	}
	now = now.Add(time.Second)
	observe(t, c, sample(apc.OnBattery, 15))
	if len(f.checks) != 1 || len(f.shutdowns) != 2 {
		t.Fatalf("expected a check and a resend: checks=%d shutdowns=%d", len(f.checks), len(f.shutdowns))
	}
	a := c.View().Actions["node-1"]
	if a.ShutdownAttempts != 2 || !a.ShutdownAccepted || a.ShutdownConfirmed || a.ShutdownGaveUp {
		t.Fatalf("unexpected action state %+v", a)
	}
	if countEvents(w, "remote_shutdown_still_running") != 1 {
		t.Fatal("missing still_running event")
	}
	// The resend restarts the verification delay.
	now = now.Add(time.Minute)
	observe(t, c, sample(apc.OnBattery, 15))
	if len(f.checks) != 1 || len(f.shutdowns) != 2 {
		t.Fatal("verified again before the delay elapsed")
	}
}

func TestUnreachableHostIsConfirmedAndLeftAlone(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{up: false}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	triggerShutdown(t, c, &now)
	now = now.Add(shutdownVerifyDelay)
	observe(t, c, sample(apc.OnBattery, 12))
	// One failed connection is not enough to trust.
	if a := c.View().Actions["node-1"]; a.ShutdownConfirmed || a.UnreachableChecks != 1 {
		t.Fatalf("confirmed on a single failed check: %+v", a)
	}
	now = now.Add(shutdownRetryDelay - time.Second)
	observe(t, c, sample(apc.OnBattery, 12))
	if len(f.checks) != 1 {
		t.Fatal("second check made before the spacing elapsed")
	}
	now = now.Add(time.Second)
	observe(t, c, sample(apc.OnBattery, 12))
	if a := c.View().Actions["node-1"]; !a.ShutdownConfirmed {
		t.Fatalf("host not confirmed down after two failed checks: %+v", a)
	}
	if countEvents(w, "remote_shutdown_confirmed") != 1 || len(f.shutdowns) != 1 {
		t.Fatal("expected one confirmed event and no resend")
	}
	for i := 0; i < 5; i++ {
		now = now.Add(10 * time.Minute)
		observe(t, c, sample(apc.OnBattery, 12))
	}
	if len(f.checks) != 2 || len(f.shutdowns) != 1 {
		t.Fatalf("confirmed host contacted again: checks=%d shutdowns=%d", len(f.checks), len(f.shutdowns))
	}
	// A confirmed host is still woken after recovery.
	observe(t, c, sample(apc.Online, 100))
	now = now.Add(31 * time.Second)
	observe(t, c, sample(apc.Online, 100))
	if len(f.wakes) != 1 {
		t.Fatalf("wake count %d", len(f.wakes))
	}
}

func TestStillRunningHostGivesUpAfterMaxAttempts(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{up: true}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	triggerShutdown(t, c, &now)
	for i := 0; i < 14; i++ {
		now = now.Add(shutdownVerifyDelay)
		observe(t, c, sample(apc.OnBattery, 12))
	}
	if len(f.shutdowns) != maxShutdownAttempts || len(f.checks) != maxShutdownAttempts {
		t.Fatalf("shutdowns=%d checks=%d, want %d each", len(f.shutdowns), len(f.checks), maxShutdownAttempts)
	}
	if got := countEvents(w, "remote_shutdown_gave_up"); got != 1 {
		t.Fatalf("gave up events %d", got)
	}
	if a := c.View().Actions["node-1"]; !a.ShutdownGaveUp || a.ShutdownConfirmed {
		t.Fatalf("unexpected action state %+v", a)
	}
}

func TestReachableAgainResetsUnreachableCount(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{up: false}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	triggerShutdown(t, c, &now)
	now = now.Add(shutdownVerifyDelay)
	observe(t, c, sample(apc.OnBattery, 12))
	if c.View().Actions["node-1"].UnreachableChecks != 1 {
		t.Fatal("first failed check not counted")
	}
	// The host answers again on the second check: the count starts over and the
	// shutdown is sent again.
	f.up = true
	now = now.Add(shutdownRetryDelay)
	observe(t, c, sample(apc.OnBattery, 12))
	a := c.View().Actions["node-1"]
	if a.UnreachableChecks != 0 || a.ShutdownConfirmed || len(f.shutdowns) != 2 {
		t.Fatalf("unexpected state %+v shutdowns=%d", a, len(f.shutdowns))
	}
}

func TestDisableDuringLivenessCheckStopsFollowUps(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{up: true}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	triggerShutdown(t, c, &now)
	now = now.Add(shutdownVerifyDelay)

	inCall := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	t.Cleanup(releaseAll)
	f.onReachable = func(Device) {
		close(inCall)
		<-release
	}
	done := observeAsync(c, sample(apc.OnBattery, 12))
	waitFor(t, inCall, "the liveness check to start")
	cfg := store.Get()
	cfg.Enabled = false
	if err := c.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	releaseAll()
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	if len(f.shutdowns) != 1 {
		t.Fatalf("shutdown resent after automation was disabled: %d calls", len(f.shutdowns))
	}
	if a := c.View().Actions["node-1"]; a.ShutdownAttempts != 1 {
		t.Fatalf("new attempt recorded after automation was disabled: %+v", a)
	}
	if countEvents(w, "remote_shutdown_still_running") != 0 {
		t.Fatal("result of the check was recorded after automation was disabled")
	}
}

func TestCancelledShutdownIsNotRecordedAsFailure(t *testing.T) {
	store, path := configured(t)
	w := &memoryWriter{}
	f := &fakeExecutor{failShutdown: true}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := makeController(t, store, path, w, f, &now)
	triggerShutdown(t, c, &now)
	for i := 0; i < maxShutdownAttempts-2; i++ {
		now = now.Add(shutdownRetryDelay)
		observe(t, c, sample(apc.OnBattery, 12))
	}
	if a := c.View().Actions["node-1"]; a.ShutdownAttempts != maxShutdownAttempts-1 {
		t.Fatalf("setup: attempts %d", a.ShutdownAttempts)
	}
	// The service is stopped while the final attempt is in flight.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.onShutdown = func(Device) { cancel() }
	now = now.Add(shutdownRetryDelay)
	if err := c.Observe(ctx, sample(apc.OnBattery, 12)); err != nil {
		t.Fatal(err)
	}
	f.onShutdown = nil
	a := c.View().Actions["node-1"]
	if a.ShutdownGaveUp || a.ShutdownAttempts != maxShutdownAttempts-1 || !a.NextShutdownRetry.IsZero() {
		t.Fatalf("interrupted attempt was consumed: %+v", a)
	}
	if got := countEvents(w, "remote_shutdown_failed"); got != maxShutdownAttempts-1 {
		t.Fatalf("failed events %d: interrupted command was recorded", got)
	}
	if countEvents(w, "remote_shutdown_gave_up") != 0 {
		t.Fatal("gave up because of a cancellation")
	}
	// After a restart the refunded attempt is made promptly and counted normally.
	c = makeController(t, store, path, w, f, &now)
	if a := c.View().Actions["node-1"]; a.ShutdownAttempts != maxShutdownAttempts-1 {
		t.Fatalf("refund not persisted: %+v", a)
	}
	observe(t, c, sample(apc.OnBattery, 12))
	a = c.View().Actions["node-1"]
	if a.ShutdownAttempts != maxShutdownAttempts || !a.ShutdownGaveUp {
		t.Fatalf("final attempt not made after restart: %+v", a)
	}
}
