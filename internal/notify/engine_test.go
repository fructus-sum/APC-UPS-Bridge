package notify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
)

type message struct{ subject, body, id string }
type fakeSender struct {
	messages []message
	fail     bool
}

func (f *fakeSender) Send(_ context.Context, _ Config, subject, body, id string) error {
	f.messages = append(f.messages, message{subject, body, id})
	if f.fail {
		return errors.New("internet offline")
	}
	return nil
}
func snap(power apc.PowerState, pct float64) monitor.Status {
	return monitor.Status{Healthy: true, Current: &apc.Snapshot{Power: power, Status: string(power), BatteryPercent: &pct}}
}
func newEngine(t *testing.T, dir string, sender *fakeSender) (*Engine, *Store) {
	t.Helper()
	store, err := OpenConfig(filepath.Join(dir, "email.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !store.Get().Enabled {
		if err = store.Save(Config{Enabled: true, Host: "smtp.example.com", Port: 587, Security: "starttls", Username: "notify@example.com", Password: "super secret app password", From: "notify@example.com", To: "admin@example.com", WaitForDevices: true}); err != nil {
			t.Fatal(err)
		}
	}
	e, err := Open(store, filepath.Join(dir, "report.json"), filepath.Join(dir, "logs"), events.FileWriter{Dir: filepath.Join(dir, "logs")}, sender)
	if err != nil {
		t.Fatal(err)
	}
	return e, store
}
func TestOutageMailSurvivesRepeatedPiRestarts(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 10, 16, 0, 0, 0, time.UTC)
	sender := &fakeSender{fail: true}
	e, _ := newEngine(t, dir, sender)
	e.now = func() time.Time { return now }
	e.probe = func(context.Context, Config) error { return nil }
	e.hosts = func(context.Context, []Target) bool { return false }
	ctx := context.Background()
	targets := []Target{{ID: "nas", Name: "NAS", Host: "192.0.2.1", Port: 22}}
	if err := e.Observe(ctx, snap(apc.Online, 100), targets); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := e.Observe(ctx, snap(apc.OnBattery, 88), targets); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("start alert count=%d", len(sender.messages))
	}
	now = now.Add(time.Minute)
	if err := e.Observe(ctx, snap(apc.OnBattery, 76), targets); err != nil {
		t.Fatal(err)
	}
	e, _ = newEngine(t, dir, sender)
	e.now = func() time.Time { return now }
	e.probe = func(context.Context, Config) error { return nil }
	e.hosts = func(context.Context, []Target) bool { return false }
	if err := e.Observe(ctx, snap(apc.OnBattery, 70), targets); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 {
		t.Fatal("outage alert resent after restart")
	}
	now = now.Add(time.Minute)
	if err := e.Observe(ctx, snap(apc.Online, 70), targets); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 2 {
		t.Fatal("restoration alert missing")
	}
	for _, pct := range []float64{88, 99.9, 100} {
		now = now.Add(time.Minute)
		if err := e.Observe(ctx, snap(apc.Online, pct), targets); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.messages) != 2 {
		t.Fatal("summary sent before hosts reachable")
	}
	// Restart a second time before all systems return. Pending incident is not lost.
	e, _ = newEngine(t, dir, sender)
	e.now = func() time.Time { return now }
	e.probe = func(context.Context, Config) error { return errors.New("internet down") }
	e.hosts = func(context.Context, []Target) bool { return true }
	now = now.Add(time.Minute)
	if err := e.Observe(ctx, snap(apc.Online, 100), targets); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 2 || !e.View().Pending {
		t.Fatal("offline report should remain queued")
	}
	e.probe = func(context.Context, Config) error { return nil }
	now = now.Add(3 * time.Minute)
	sender.fail = false
	if err := e.Observe(ctx, snap(apc.Online, 100), targets); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 3 || !strings.Contains(sender.messages[2].body, "Pi/service restarts while pending: 2") {
		t.Fatalf("report missing restarts: %+v", sender.messages)
	}
	if e.View().Pending {
		t.Fatal("report still pending after SMTP success")
	}
	now = now.Add(time.Minute)
	_ = e.Observe(ctx, snap(apc.Online, 100), targets)
	if len(sender.messages) != 3 {
		t.Fatal("final report repeated after completion")
	}
}
func TestMultipleOutagesBecomeOneFinalReport(t *testing.T) {
	dir := t.TempDir()
	s := &fakeSender{}
	e, _ := newEngine(t, dir, s)
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	e.probe = func(context.Context, Config) error { return nil }
	e.hosts = func(context.Context, []Target) bool { return true }
	ctx := context.Background()
	seq := []struct {
		p      apc.PowerState
		charge float64
	}{{apc.Online, 100}, {apc.OnBattery, 82}, {apc.Online, 70}, {apc.OnBattery, 53}, {apc.Online, 50}, {apc.Online, 99.9}, {apc.Online, 100}}
	for _, step := range seq {
		now = now.Add(time.Minute)
		if err := e.Observe(ctx, snap(step.p, step.charge), nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.messages) != 3 {
		t.Fatalf("got %d mails, expected start and restored alert for the first outage (second is inside the cooldown) + 1 summary", len(s.messages))
	}
	if !strings.Contains(s.messages[2].body, "Power interruptions: 2") {
		t.Fatal("missing combined report")
	}
}
func TestStoreSecretIsNotInViewAndPersistedRestricted(t *testing.T) {
	dir := t.TempDir()
	s, _ := newEngine(t, dir, &fakeSender{})
	v := s.Config()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "super secret") || strings.Contains(string(data), "\"password\":") {
		t.Fatalf("secret exposed in view: %s", data)
	}
	if !v.HasPassword {
		t.Fatal("expected password saved")
	}
	st, err := os.Stat(filepath.Join(dir, "email.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("file mode: %v", st.Mode())
	}
	c := DefaultConfig()
	c.Enabled = true
	c.Host = "smtp.example.com"
	c.Security = "starttls"
	c.Port = 587
	c.Username = "notify@example.com"
	c.From = "notify@example.com"
	c.To = "admin@example.com"
	if err := s.SaveConfig(c); err != nil {
		t.Fatalf("blank password should preserve saved value: %v", err)
	}
	if !s.Config().HasPassword {
		t.Fatal("saved password cleared accidentally")
	}
	c.Security = "none"
	if err := s.SaveConfig(c); err == nil {
		t.Fatal("plaintext SMTP accepted")
	}
}

func TestSummaryFailureIsQueuedAndThrottled(t *testing.T) {
	dir := t.TempDir()
	sender := &fakeSender{}
	e, _ := newEngine(t, dir, sender)
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	e.probe = func(context.Context, Config) error { return nil }
	e.hosts = func(context.Context, []Target) bool { return true }
	ctx := context.Background()
	_ = e.Observe(ctx, snap(apc.Online, 100), nil)
	now = now.Add(time.Minute)
	_ = e.Observe(ctx, snap(apc.OnBattery, 79), nil)
	now = now.Add(time.Minute)
	_ = e.Observe(ctx, snap(apc.Online, 60), nil)
	sender.fail = true
	now = now.Add(time.Minute)
	_ = e.Observe(ctx, snap(apc.Online, 100), nil)
	if !e.View().Pending {
		t.Fatal("report lost after SMTP failed")
	}
	attempts := len(sender.messages)
	for k := 0; k < 4; k++ {
		now = now.Add(time.Minute)
		_ = e.Observe(ctx, snap(apc.Online, 100), nil)
	}
	if len(sender.messages) != attempts {
		t.Fatal("summary retried too quickly")
	}
	e, _ = newEngine(t, dir, sender)
	e.now = func() time.Time { return now }
	e.probe = func(context.Context, Config) error { return nil }
	e.hosts = func(context.Context, []Target) bool { return true }
	now = now.Add(11 * time.Minute)
	sender.fail = false
	_ = e.Observe(ctx, snap(apc.Online, 100), nil)
	if e.View().Pending {
		t.Fatal("report not delivered after recovery")
	}
}

func cooldownEngine(t *testing.T) (*Engine, *fakeSender, *time.Time) {
	t.Helper()
	sender := &fakeSender{}
	e, _ := newEngine(t, t.TempDir(), sender)
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }
	e.probe = func(context.Context, Config) error { return nil }
	e.hosts = func(context.Context, []Target) bool { return true }
	return e, sender, &now
}
func observe(t *testing.T, e *Engine, p apc.PowerState, pct float64) {
	t.Helper()
	if err := e.Observe(context.Background(), snap(p, pct), nil); err != nil {
		t.Fatal(err)
	}
}

func TestRapidFlappingSendsOneStartAndOneRestore(t *testing.T) {
	e, sender, now := cooldownEngine(t)
	observe(t, e, apc.Online, 100)
	for k := 0; k < 3; k++ {
		*now = now.Add(30 * time.Second)
		observe(t, e, apc.OnBattery, 90)
		*now = now.Add(30 * time.Second)
		observe(t, e, apc.Online, 90)
		// Re-polling while on battery or online must not resend.
		*now = now.Add(30 * time.Second)
		observe(t, e, apc.Online, 91)
	}
	if len(sender.messages) != 2 || !strings.Contains(sender.messages[0].subject, "Power failure") || !strings.Contains(sender.messages[1].subject, "restored") {
		t.Fatalf("expected one start and one restore, got %+v", sender.messages)
	}
	inc := e.state.Current
	if len(inc.Periods) != 3 || inc.Periods[0].AlertSuppressed || !inc.Periods[1].AlertSuppressed || !inc.Periods[2].AlertSuppressed {
		t.Fatalf("unexpected periods: %+v", inc.Periods)
	}
	observe(t, e, apc.Online, 100)
	*now = now.Add(3 * time.Minute)
	observe(t, e, apc.Online, 100)
	last := sender.messages[len(sender.messages)-1]
	if len(sender.messages) != 3 || !strings.Contains(last.body, "Power interruptions: 3") {
		t.Fatalf("summary must still cover every period: %+v", sender.messages)
	}
}

func TestNewOutageAfterCooldownAlertsAgain(t *testing.T) {
	e, sender, now := cooldownEngine(t)
	observe(t, e, apc.Online, 100)
	*now = now.Add(time.Minute)
	observe(t, e, apc.OnBattery, 90)
	*now = now.Add(time.Minute)
	observe(t, e, apc.Online, 90)
	if len(sender.messages) != 2 {
		t.Fatalf("got %d", len(sender.messages))
	}
	*now = now.Add(15 * time.Minute)
	observe(t, e, apc.OnBattery, 80)
	if len(sender.messages) != 3 || !strings.Contains(sender.messages[2].subject, "Power failure") {
		t.Fatalf("second outage not alerted: %+v", sender.messages)
	}
	*now = now.Add(time.Minute)
	observe(t, e, apc.Online, 80)
	if len(sender.messages) != 4 {
		t.Fatalf("second restore not alerted: %+v", sender.messages)
	}
}

func TestSuppressedOutageAlertsOnceCooldownPasses(t *testing.T) {
	e, sender, now := cooldownEngine(t)
	observe(t, e, apc.Online, 100)
	*now = now.Add(time.Minute)
	observe(t, e, apc.OnBattery, 90)
	*now = now.Add(time.Minute)
	observe(t, e, apc.Online, 90)
	*now = now.Add(time.Minute)
	observe(t, e, apc.OnBattery, 85)
	*now = now.Add(5 * time.Minute)
	observe(t, e, apc.OnBattery, 80)
	if len(sender.messages) != 2 {
		t.Fatalf("alert sent inside the cooldown: %+v", sender.messages)
	}
	if p := e.state.Current.Periods[1]; p.StartAttempted || p.AlertSuppressed {
		t.Fatalf("waiting period must stay unattempted: %+v", p)
	}
	*now = now.Add(6 * time.Minute)
	observe(t, e, apc.OnBattery, 75)
	if len(sender.messages) != 3 || !strings.Contains(sender.messages[2].subject, "Power failure") {
		t.Fatalf("start alert missing after cooldown: %+v", sender.messages)
	}
	*now = now.Add(time.Minute)
	observe(t, e, apc.OnBattery, 70)
	if len(sender.messages) != 3 {
		t.Fatal("start alert repeated")
	}
	*now = now.Add(time.Minute)
	observe(t, e, apc.Online, 70)
	if len(sender.messages) != 4 || !strings.Contains(sender.messages[3].subject, "restored") {
		t.Fatalf("restore alert missing for an alerted period: %+v", sender.messages)
	}
}
