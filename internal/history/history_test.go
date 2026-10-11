package history

import (
	"encoding/json"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"os"
	"path/filepath"
	"strings"
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
	if got.Points[0].Observed {
		t.Fatal("missing bucket rendered as observed")
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
	for _, age := range []int{29, 1} {
		if _, err := os.Stat(filepath.Join(dir, filename(now.AddDate(0, 0, -age)))); err != nil {
			t.Fatalf("recent file %d days old removed: %v", age, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, filename(now.AddDate(0, 0, -35)))); !os.IsNotExist(err) {
		t.Fatalf("old file survived: %v", err)
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

func TestTornLastLineThenRecord(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Add(-2 * time.Hour).Truncate(Interval).Add(time.Minute)
	prev := now.Add(-Interval)
	path := filepath.Join(dir, filename(now))
	good, _ := json.Marshal(Sample{Time: prev, InputVolts: number(229)})
	torn := string(good) + "\n" + `{"time":"` + prev.Add(-Interval).Format(time.RFC3339) + `","input_vo`
	if err := os.WriteFile(path, []byte(torn), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	if err := s.Record(&apc.Snapshot{ObservedAt: now, InputVolts: number(231)}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(1, now.Add(time.Minute), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Samples != 2 {
		t.Fatalf("expected the old and new sample, got %d", got.Samples)
	}
	found := false
	for _, p := range got.Points {
		if p.Observed && p.InputVolts != nil && *p.InputVolts == 231 && p.Time.Equal(now.Truncate(Interval)) {
			found = true
		}
	}
	if !found {
		t.Fatal("new sample not readable after torn line")
	}
}

func TestCorruptAndOversizeLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Add(-2 * time.Hour).Truncate(Interval).Add(time.Minute)
	prev := now.Add(-Interval)
	path := filepath.Join(dir, filename(now))
	good, _ := json.Marshal(Sample{Time: prev, InputVolts: number(229)})
	content := "not json\n" + strings.Repeat("x", 3*maxLine) + "\n" + string(good) + "\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	got, err := s.Read(1, now, "")
	if err != nil || got.Samples != 1 {
		t.Fatalf("read with bad lines: samples=%d err=%v", got.Samples, err)
	}
	if err := s.Record(&apc.Snapshot{ObservedAt: now, InputVolts: number(231)}); err != nil {
		t.Fatalf("record with bad lines: %v", err)
	}
	got, err = s.Read(1, now, "")
	if err != nil || got.Samples != 2 {
		t.Fatalf("after record: samples=%d err=%v", got.Samples, err)
	}
}

func TestReadMissingDirectory(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "absent"))
	got, err := s.Read(7, time.Now(), "")
	if err != nil || got.Samples != 0 {
		t.Fatalf("missing dir: samples=%d err=%v", got.Samples, err)
	}
}
