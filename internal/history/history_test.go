package history

import (
	"encoding/json"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func number(f float64) *float64 { return &f }
func TestRecordsDedupeAcrossRebootAndMissingGaps(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Add(-2 * time.Hour).Truncate(Interval).Add(3 * time.Minute)
	a := New(dir)
	if err := a.Record(&apc.Snapshot{ObservedAt: now, InputVolts: number(230), LoadPercent: number(19)}); err != nil {
		t.Fatal(err)
	}
	b := New(dir)
	if err := b.Record(&apc.Snapshot{ObservedAt: now.Add(2 * time.Minute), InputVolts: number(238), LoadPercent: number(25)}); err != nil {
		t.Fatal(err)
	}
	next := now.Add(2 * Interval)
	if err := b.Record(&apc.Snapshot{ObservedAt: next, InputVolts: number(231), LoadPercent: number(21)}); err != nil {
		t.Fatal(err)
	}
	got, err := b.Read(1, next.Add(time.Minute), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Samples != 2 {
		t.Fatalf("expected 2 not duplicate, got %d", got.Samples)
	}
	for i := 1; i < len(got.Points); i++ {
		if got.Points[i].Observed && got.Points[i-1].Observed {
			t.Fatal("unexpected continuous observations")
		}
	}
	for _, p := range got.Points {
		if p.Observed && p.InputVolts != nil && *p.InputVolts == 238 {
			t.Fatal("duplicate overwrote first observation")
		}
	}
}
func TestEventTimesNoMadeUpMetrics(t *testing.T) {
	dir := t.TempDir()
	logs := t.TempDir()
	now := time.Now().UTC().Truncate(Interval)
	w := events.FileWriter{Dir: logs}
	for _, e := range []events.Event{{Time: now.Add(-2 * time.Hour), Type: "power_lost"}, {Time: now.Add(-time.Hour), Type: "power_restored"}} {
		if err := w.Write(e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := New(dir).Read(14, now, logs)
	if err != nil {
		t.Fatal(err)
	}
	if got.Samples != 0 || len(got.Events) != 2 {
		t.Fatalf("bad empty history: samples=%d events=%d", got.Samples, len(got.Events))
	}
	if !got.Events[0].Time.Before(got.Events[1].Time) {
		t.Fatal("events not chronological")
	}
	if got.From.Add(14*24*time.Hour) != got.To {
		t.Fatal("time axis not fixed")
	}
	b, _ := json.Marshal(got.Points[0])
	if string(b) == "" || got.Points[0].Observed {
		t.Fatal("missing bucket not rendered")
	}
}
func TestRetentionAndValidation(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().UTC().Truncate(24 * time.Hour)
	for _, age := range []int{35, 29, 1} {
		name := filename(now.AddDate(0, 0, -age))
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0600); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Cleanup(now)
	if err != nil || n != 1 {
		t.Fatalf("cleanup %d %v", n, err)
	}
	for _, days := range []int{0, 31, -1, 100} {
		if _, err := s.Read(days, now, ""); err == nil {
			t.Fatal("accepted bad range")
		}
	}
	snap := &apc.Snapshot{ObservedAt: now, LoadPercent: number(101)}
	if err := s.Record(snap); err == nil {
		t.Fatal("accepted invalid percentage")
	}
}
