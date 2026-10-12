package events

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadHistoryAndReport(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	wr := FileWriter{Dir: dir}
	for _, ev := range []Event{
		{Time: now.Add(-20 * time.Minute), Type: "power_lost", Level: "warning"},
		{Time: now.Add(-10 * time.Minute), Type: "low_battery", Level: "warning", Details: map[string]any{"battery_percent": 19}},
		{Time: now, Type: "power_restored", Level: "info", Details: map[string]any{"observed_outage_seconds": 1200}},
	} {
		if err := wr.Write(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "events-2026-10.jsonl"), []byte("{broken json\n"), 0600); err != nil {
		t.Fatal(err)
	} // intentionally broken file
	history, err := ReadHistory(dir, 30, 20, now)
	if err != nil {
		t.Fatal(err)
	}
	if history.Report.Outages != 0 || len(history.Events) != 0 {
		t.Fatalf("corrupt file should not invent events: %+v", history)
	}
	// Rebuild valid lines for report.
	_ = os.Remove(filepath.Join(dir, "events-2026-10.jsonl"))
	for _, ev := range []Event{
		{Time: now.Add(-20 * time.Minute), Type: "power_lost", Level: "warning"},
		{Time: now.Add(-10 * time.Minute), Type: "low_battery", Level: "warning"},
		{Time: now, Type: "power_restored", Level: "info", Details: map[string]any{"observed_outage_seconds": 1200}},
	} {
		if err := wr.Write(ev); err != nil {
			t.Fatal(err)
		}
	}
	history, err = ReadHistory(dir, 30, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if history.Report.Outages != 1 || history.Report.Restorations != 1 || history.Report.ObservedOutageSeconds != 1200 || len(history.Events) != 2 {
		t.Fatalf("report: %+v", history)
	}
	if history.Events[0].Type != "power_restored" {
		t.Fatal("newest event not first")
	}
}

func TestReadPowerTimelineKeepsNewestWhenOverCap(t *testing.T) {
	old := maxPowerTimelineEvents
	maxPowerTimelineEvents = 5
	defer func() { maxPowerTimelineEvents = old }()
	dir := t.TempDir()
	w := FileWriter{Dir: dir}
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for i := 0; i < 13; i++ {
		if err := w.Write(Event{Time: base.Add(time.Duration(i) * time.Second), Type: "power_lost", Details: map[string]any{"n": float64(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadPowerTimeline(dir, base.Add(-time.Hour), time.Now().UTC())
	if err != nil {
		t.Fatalf("over-cap window must not fail: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 events, got %d", len(got))
	}
	for i, e := range got {
		if e.Details["n"] != float64(8+i) {
			t.Fatalf("expected newest events oldest-first, got %+v", got)
		}
	}
}
