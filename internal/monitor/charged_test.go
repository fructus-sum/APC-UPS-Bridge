package monitor

import (
	"context"
	"errors"
	"testing"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
)

func TestFullChargeEventOnlyAfterObservedRecharge(t *testing.T) {
	src := &source{samples: []sample{
		{"ONLINE", pct(100), nil}, {"ONBATT", pct(80), nil}, {"ONLINE", pct(81), nil},
		{"ONLINE", pct(99), nil}, {"ONLINE", pct(100), nil}, {"ONLINE", pct(100), nil},
	}}
	rec := &recorder{}
	mon, err := New(src, rec, 20)
	if err != nil {
		t.Fatal(err)
	}
	for range src.samples {
		if err := mon.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for _, e := range rec.items {
		if e.Type == "battery_fully_charged" {
			count++
			if e.Details["battery_percent"] != 100.0 {
				t.Fatalf("event must record actual charge: %+v", e.Details)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected one full recharge marker, got %d in %+v", count, rec.items)
	}
}

func countFullCharge(items []events.Event) int {
	n := 0
	for _, e := range items {
		if e.Type == "battery_fully_charged" {
			n++
		}
	}
	return n
}

func TestFullChargeNotReportedAcrossCommunicationGap(t *testing.T) {
	src := &source{samples: []sample{
		{"ONLINE", pct(80), nil}, {"", nil, errors.New("no link")}, {"ONLINE", pct(100), nil},
	}}
	rec := &recorder{}
	mon, err := New(src, rec, 20)
	if err != nil {
		t.Fatal(err)
	}
	for range src.samples {
		_ = mon.Poll(context.Background())
	}
	if n := countFullCharge(rec.items); n != 0 {
		t.Fatalf("recharge across an unobserved gap was reported: %+v", rec.items)
	}
}

func TestFullChargeIgnoresJitterNearFull(t *testing.T) {
	src := &source{samples: []sample{
		{"ONLINE", pct(90), nil}, {"ONLINE", pct(100), nil},
		{"ONLINE", pct(99), nil}, {"ONLINE", pct(100), nil}, {"ONLINE", pct(99), nil}, {"ONLINE", pct(100), nil},
		{"ONLINE", pct(95), nil}, {"ONLINE", pct(100), nil},
	}}
	rec := &recorder{}
	mon, err := New(src, rec, 20)
	if err != nil {
		t.Fatal(err)
	}
	for range src.samples {
		if err := mon.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := countFullCharge(rec.items); n != 2 {
		t.Fatalf("expected events only after 90%% and 95%%, got %d in %+v", n, rec.items)
	}
}
