package power

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
)

type fakeExecutor struct {
	shutdowns    []string
	wakes        []string
	failShutdown bool
	failWake     bool
}

func (f *fakeExecutor) Shutdown(_ context.Context, d Device) error {
	f.shutdowns = append(f.shutdowns, d.ID)
	if f.failShutdown {
		return errors.New("ssh failed")
	}
	return nil
}
func (f *fakeExecutor) Wake(_ context.Context, d Device) error {
	f.wakes = append(f.wakes, d.ID)
	if f.failWake {
		return errors.New("udp failed")
	}
	return nil
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
func TestFailureNoRetryOrWake(t *testing.T) {
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
		t.Fatal("failed command retried")
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
	if len(f.shutdowns) != 0 {
		t.Fatal("acted without audit logging")
	}
	w.fail = false
	observe(t, c, sample(apc.OnBattery, 15))
	if len(f.shutdowns) != 0 {
		t.Fatal("retry despite persisted intent")
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
