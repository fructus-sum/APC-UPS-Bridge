package events

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOnDemandEvents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	w := FileWriter{Dir: dir}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("log directory should not exist before an event")
	}
	stamp := time.Date(2026, 10, 10, 12, 0, 0, 0, time.FixedZone("BST", 3600))
	if err := w.Write(Event{Time: stamp, Type: "power_lost", Level: "warning", Details: map[string]any{"battery_percent": 95.0}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "events-2026-10.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(data, []byte("\n")) != 1 {
		t.Fatal("expected one JSON line", string(data))
	}
	var event Event
	if err := json.Unmarshal(bytes.TrimSpace(data), &event); err != nil || event.Time.Location() != time.UTC {
		t.Fatal(event, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
}
func TestCleanupOnlyExpiredMonths(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"events-2025-01.jsonl", "events-2026-09.jsonl", "events-2026-10.jsonl", "notes.txt", "events-2026-11.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	n, err := Cleanup(dir, 365, now)
	if err != nil || n != 1 {
		t.Fatalf("removed=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "events-2025-01.jsonl")); !os.IsNotExist(err) {
		t.Fatal("expired log remains")
	}
	for _, name := range []string{"events-2026-09.jsonl", "events-2026-10.jsonl", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestUnlimitedRetention(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "events-2020-01.jsonl")
	if err := os.WriteFile(p, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	n, err := Cleanup(dir, 0, time.Now())
	if n != 0 || err != nil {
		t.Fatal(n, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}
