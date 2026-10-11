// Package history records small, bounded UPS voltage and load samples. It is
// separate from event-only logs and never estimates unobserved measurements.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
)

const Interval = 15 * time.Minute
const Retention = 30 * 24 * time.Hour
const MaxDays = 30
const DefaultDays = 14

type Sample struct {
	Time        time.Time `json:"time"`
	InputVolts  *float64  `json:"input_volts"`
	OutputVolts *float64  `json:"output_volts"`
	LoadPercent *float64  `json:"load_percent"`
}

type Bucket struct {
	Time        time.Time `json:"time"`
	InputVolts  *float64  `json:"input_volts"`
	OutputVolts *float64  `json:"output_volts"`
	LoadPercent *float64  `json:"load_percent"`
	Observed    bool      `json:"observed"`
}

type Result struct {
	From            time.Time      `json:"from"`
	To              time.Time      `json:"to"`
	IntervalSeconds int            `json:"interval_seconds"`
	Points          []Bucket       `json:"points"`
	Events          []events.Event `json:"events"`
	Samples         int            `json:"samples"`
}

type Store struct {
	mu         sync.Mutex
	Dir        string
	lastBucket time.Time
}

func New(dir string) *Store { return &Store{Dir: dir} }

func filename(t time.Time) string { return "samples-" + t.UTC().Format("2006-01-02") + ".jsonl" }

func dayFile(name string) (time.Time, bool) {
	if len(name) != len("samples-2006-01-02.jsonl") || !strings.HasPrefix(name, "samples-") || !strings.HasSuffix(name, ".jsonl") {
		return time.Time{}, false
	}
	d, err := time.Parse("2006-01-02", strings.TrimSuffix(strings.TrimPrefix(name, "samples-"), ".jsonl"))
	return d, err == nil
}

// Record writes a maximum of one healthy observation per UTC 15-minute bucket.
// It never stores a fabricated zero for an unavailable UPS metric.
// Reboots inside a bucket do not duplicate its sample.
func (s *Store) Record(snap *apc.Snapshot) error {
	if s == nil || snap == nil || s.Dir == "" {
		return nil
	}
	now := snap.ObservedAt.UTC()
	if now.IsZero() {
		return fmt.Errorf("missing observation time")
	}
	if now.After(time.Now().UTC().Add(5 * time.Minute)) {
		return fmt.Errorf("observation in future")
	}
	if !valid(snap.InputVolts, 0, 1000) || !valid(snap.OutputVolts, 0, 1000) || !valid(snap.LoadPercent, 0, 100) {
		return fmt.Errorf("invalid UPS sample")
	}
	if snap.InputVolts == nil && snap.OutputVolts == nil && snap.LoadPercent == nil {
		return nil
	}
	bucket := now.Truncate(Interval)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastBucket.Equal(bucket) {
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(s.Dir, filename(now))
	// Check last valid sample in the day's small log, including across service restarts.
	if last, err := lastSampleBucket(path); err != nil {
		return err
	} else if last.Equal(bucket) {
		s.lastBucket = bucket
		return nil
	}
	rec := Sample{Time: now, InputVolts: clone(snap.InputVolts), OutputVolts: clone(snap.OutputVolts), LoadPercent: clone(snap.LoadPercent)}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(b, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	s.lastBucket = bucket
	return nil
}
func valid(p *float64, min, max float64) bool {
	return p == nil || (!math.IsNaN(*p) && !math.IsInf(*p, 0) && *p >= min && *p <= max)
}
func clone(p *float64) *float64 {
	if p == nil {
		return nil
	}
	x := *p
	return &x
}
func lastSampleBucket(path string) (time.Time, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 1024), 8192)
	var last time.Time
	for scan.Scan() {
		var v Sample
		if json.Unmarshal(scan.Bytes(), &v) == nil && !v.Time.IsZero() {
			last = v.Time.UTC().Truncate(Interval)
		}
	}
	return last, scan.Err()
}

// Cleanup deletes whole dated files strictly before the oldest allowed UTC day.
// Older data remaining in the boundary file are excluded by Read.
func (s *Store) Cleanup(now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cutoff := now.UTC().Add(-Retention).Truncate(24 * time.Hour)
	n := 0
	for _, ent := range items {
		day, ok := dayFile(ent.Name())
		if !ok || !ent.Type().IsRegular() || !day.Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(s.Dir, ent.Name())); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Read returns a fixed, regularly spaced UTC time grid. Missing observations
// remain null, so a stopped Pi or missing UPS communication renders as a gap.
// Event data are added for power-state annotations, not used to invent voltage.
func (s *Store) Read(days int, now time.Time, eventDir string) (Result, error) {
	if days != 1 && days != 7 && days != 14 && days != 30 {
		return Result{}, fmt.Errorf("range must be 1, 7, 14 or 30 days")
	}
	now = now.UTC()
	end := now.Truncate(Interval)
	start := end.Add(-time.Duration(days) * 24 * time.Hour)
	count := days*24*int(time.Hour/Interval) + 1
	result := Result{From: start, To: end, IntervalSeconds: int(Interval.Seconds()), Points: make([]Bucket, count), Events: []events.Event{}}
	for i := range result.Points {
		result.Points[i].Time = start.Add(time.Duration(i) * Interval)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := os.ReadDir(s.Dir)
	if !errors.Is(err, os.ErrNotExist) && err != nil {
		return Result{}, err
	}
	files := make([]string, 0)
	for _, ent := range items {
		if day, ok := dayFile(ent.Name()); ok && ent.Type().IsRegular() && !day.After(end) && day.Add(24*time.Hour).After(start) {
			files = append(files, ent.Name())
		}
	}
	sort.Strings(files)
	for _, name := range files {
		f, err := os.Open(filepath.Join(s.Dir, name))
		if err != nil {
			return Result{}, err
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 1024), 8192)
		for scan.Scan() {
			var v Sample
			if json.Unmarshal(scan.Bytes(), &v) != nil || v.Time.IsZero() || !valid(v.InputVolts, 0, 1000) || !valid(v.OutputVolts, 0, 1000) || !valid(v.LoadPercent, 0, 100) {
				continue
			}
			slot := v.Time.UTC().Truncate(Interval)
			if slot.Before(start) || slot.After(end) {
				continue
			}
			idx := int(slot.Sub(start) / Interval)
			// Keep first measured observation in the bucket; do not average through gaps.
			if result.Points[idx].Observed {
				continue
			}
			result.Points[idx].InputVolts = v.InputVolts
			result.Points[idx].OutputVolts = v.OutputVolts
			result.Points[idx].LoadPercent = v.LoadPercent
			result.Points[idx].Observed = true
			result.Samples++
		}
		e := scan.Err()
		c := f.Close()
		if e != nil {
			return Result{}, e
		}
		if c != nil {
			return Result{}, c
		}
	}
	if eventDir != "" {
		hist, err := events.ReadHistory(eventDir, days, 500, now)
		if err != nil {
			return Result{}, err
		}
		for i := len(hist.Events) - 1; i >= 0; i-- {
			ev := hist.Events[i]
			if ev.Time.Before(start) || ev.Time.After(now) {
				continue
			}
			switch ev.Type {
			case "power_lost", "power_restored", "on_battery_detected", "ups_communication_lost", "ups_communication_restored":
				result.Events = append(result.Events, ev)
			}
		}
	}
	return result, nil
}
