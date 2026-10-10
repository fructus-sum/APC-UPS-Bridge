package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
)

type sample struct {
	status string
	charge *float64
	err    error
}
type source struct {
	samples []sample
	next    int
}

func (s *source) Read(context.Context) (apc.Snapshot, error) {
	if s.next >= len(s.samples) {
		panic("exhausted test samples")
	}
	x := s.samples[s.next]
	s.next++
	if x.err != nil {
		return apc.Snapshot{}, x.err
	}
	state := apc.Online
	if x.status == "ONBATT" {
		state = apc.OnBattery
	}
	if x.status == "COMMLOST" {
		state = apc.Unknown
	}
	return apc.Snapshot{Status: x.status, Power: state, Flags: []string{x.status}, BatteryPercent: x.charge}, nil
}

type recorder struct{ items []events.Event }

func (r *recorder) Write(e events.Event) error { r.items = append(r.items, e); return nil }
func pct(x float64) *float64                   { return &x }
func TestNormalPollingHasNoEvents(t *testing.T) {
	src := &source{samples: []sample{{"ONLINE", pct(100), nil}, {"ONLINE", pct(99), nil}, {"ONLINE", pct(99), nil}}}
	r := &recorder{}
	mon, _ := New(src, r, 20)
	for range src.samples {
		if err := mon.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.items) != 0 {
		t.Fatalf("normal polls wrote events: %v", r.items)
	}
	if !mon.Snapshot().Healthy {
		t.Fatal("status unhealthy")
	}
}
func TestOutageAndRestoration(t *testing.T) {
	src := &source{samples: []sample{{"ONLINE", pct(100), nil}, {"ONBATT", pct(90), nil}, {"ONBATT", pct(19), nil}, {"ONBATT", pct(15), nil}, {"ONBATT", pct(15), nil}, {"ONLINE", pct(20), nil}, {"ONLINE", pct(55), nil}}}
	r := &recorder{}
	mon, _ := New(src, r, 20)
	when := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	mon.now = func() time.Time { return when }
	for range src.samples {
		if err := mon.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		when = when.Add(10 * time.Second)
	}
	expected := []string{"power_lost", "low_battery", "power_restored"}
	if len(r.items) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, r.items)
	}
	for i, v := range expected {
		if r.items[i].Type != v {
			t.Fatalf("event %d = %s", i, r.items[i].Type)
		}
	}
	details := r.items[2].Details
	if details["observed_outage_seconds"] != 40 || details["lowest_observed_battery_percent"] != 15.0 {
		t.Fatalf("wrong summary: %+v", details)
	}
}
func TestFirstReadingOnBattery(t *testing.T) {
	src := &source{samples: []sample{{"ONBATT", pct(5), nil}, {"ONBATT", pct(4), nil}}}
	r := &recorder{}
	mon, _ := New(src, r, 20)
	for range src.samples {
		if err := mon.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.items) != 2 || r.items[0].Type != "on_battery_detected" || r.items[1].Type != "low_battery" {
		t.Fatal(r.items)
	}
}
func TestCommunicationErrorsAreConsolidated(t *testing.T) {
	src := &source{samples: []sample{{"", nil, errors.New("cannot reach apcupsd")}, {"", nil, errors.New("cannot reach apcupsd")}, {"ONLINE", pct(100), nil}}}
	r := &recorder{}
	mon, _ := New(src, r, 20)
	if err := mon.Poll(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	st := mon.Snapshot()
	if st.Healthy || st.Current != nil || st.Error == "" {
		t.Fatal(st)
	}
	if err := mon.Poll(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if err := mon.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.items) != 2 || r.items[0].Type != "ups_communication_lost" || r.items[1].Type != "ups_communication_restored" {
		t.Fatal(r.items)
	}
}
func TestLastGoodNotFreshAfterError(t *testing.T) {
	src := &source{samples: []sample{{"ONLINE", pct(100), nil}, {"", nil, errors.New("offline")}}}
	r := &recorder{}
	mon, _ := New(src, r, 20)
	if err := mon.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := mon.Poll(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	st := mon.Snapshot()
	if st.Current != nil || st.LastGood == nil || *st.LastGood.BatteryPercent != 100 || st.Healthy {
		t.Fatalf("wrong cached status %+v", st)
	}
}
func TestUnknownStatusIsFault(t *testing.T) {
	src := &source{samples: []sample{{"COMMLOST", nil, nil}}}
	r := &recorder{}
	mon, _ := New(src, r, 20)
	if err := mon.Poll(context.Background()); err == nil {
		t.Fatal("unknown state should be unhealthy")
	}
	if len(r.items) != 1 || r.items[0].Type != "ups_communication_lost" {
		t.Fatal(r.items)
	}
}
func TestThresholdValidation(t *testing.T) {
	for _, v := range []float64{-1, 101} {
		if _, err := New(&source{}, &recorder{}, v); err == nil {
			t.Fatal(v)
		}
	}
}

func TestCommunicationGapDoesNotInventPowerLossTime(t *testing.T) {
	src := &source{samples: []sample{
		{"ONLINE", pct(100), nil},
		{"", nil, errors.New("cable disconnected")},
		{"ONBATT", pct(80), nil},
		{"ONLINE", pct(90), nil},
	}}
	rec := &recorder{}
	mon, _ := New(src, rec, 20)
	for range src.samples {
		_ = mon.Poll(context.Background())
	}
	if len(rec.items) != 4 || rec.items[2].Type != "on_battery_detected" {
		t.Fatalf("must not claim precise outage start after communications gap: %+v", rec.items)
	}
}

func TestSnapshotCannotMutateCachedReadings(t *testing.T) {
	src := &source{samples: []sample{{"ONLINE", pct(100), nil}}}
	rec := &recorder{}
	mon, _ := New(src, rec, 20)
	if err := mon.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	copy := mon.Snapshot()
	*copy.Current.BatteryPercent = 0
	copy.Current.Flags[0] = "ONBATT"
	next := mon.Snapshot()
	if *next.Current.BatteryPercent != 100 || next.Current.Flags[0] != "ONLINE" {
		t.Fatal("snapshot mutated internal state")
	}
}
