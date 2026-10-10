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
