// Package history records small, bounded UPS voltage and load samples. It is
// separate from event-only logs and never estimates unobserved measurements.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	BatteryPercent *float64 `json:"battery_percent,omitempty"`
	RuntimeMinutes *float64 `json:"runtime_minutes,omitempty"`
	Power apc.PowerState `json:"power,omitempty"`
}

type Bucket struct {
	Time        time.Time `json:"time"`
	InputVolts  *float64  `json:"input_volts"`
	OutputVolts *float64  `json:"output_volts"`
	LoadPercent *float64  `json:"load_percent"`
	Observed    bool      `json:"observed"`
	BatteryPercent *float64 `json:"battery_percent,omitempty"`
	RuntimeMinutes *float64 `json:"runtime_minutes,omitempty"`
	Power apc.PowerState `json:"power,omitempty"`
	BatteryFromEvent bool `json:"battery_from_event,omitempty"`
	RuntimeFromEvent bool `json:"runtime_from_event,omitempty"`
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
	if !valid(snap.InputVolts, 0, 1000) || !valid(snap.OutputVolts, 0, 1000) || !valid(snap.LoadPercent, 0, 100) || !valid(snap.BatteryPercent, 0, 100) || !valid(snap.RuntimeMinutes, 0, 100000) {
		return fmt.Errorf("invalid UPS sample")
	}
	if snap.InputVolts == nil && snap.OutputVolts == nil && snap.LoadPercent == nil && snap.BatteryPercent == nil && snap.RuntimeMinutes == nil {
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
	rec := Sample{Time: now, InputVolts: clone(snap.InputVolts), OutputVolts: clone(snap.OutputVolts), LoadPercent: clone(snap.LoadPercent), BatteryPercent: clone(snap.BatteryPercent), RuntimeMinutes: clone(snap.RuntimeMinutes), Power: snap.Power}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	out := append(b, '\n')
	torn, tornErr := needsNewline(f)
	if tornErr != nil {
		_ = f.Close()
		return tornErr
	}
	if torn {
		// A power cut left a partial last line; keep the new sample on its own line.
		out = append([]byte{'\n'}, out...)
	}
	_, writeErr := f.Write(out)
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

const maxLine = 8192

// eachLine calls fn for every complete or trailing line of the file. Lines
// longer than maxLine are skipped. A missing file is not an error.
func eachLine(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, maxLine)
	skipping := false
	for {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			skipping = true
			continue
		}
		if len(line) > 0 && !skipping {
			fn(line)
		}
		if err == nil {
			skipping = false
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func lastSampleBucket(path string) (time.Time, error) {
	var last time.Time
	err := eachLine(path, func(b []byte) {
		var v Sample
		if json.Unmarshal(b, &v) == nil && !v.Time.IsZero() {
			last = v.Time.UTC().Truncate(Interval)
		}
	})
	return last, err
}

// needsNewline reports whether the file is non-empty and does not end in a
// newline, as left by an interrupted write.
func needsNewline(f *os.File) (bool, error) {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return false, err
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], st.Size()-1); err != nil {
		return false, err
	}
	return last[0] != '\n', nil
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
	// Day files are append-only, so reading takes no lock and never blocks
	// Record. A partially written last line is skipped by eachLine's caller.
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
		err := eachLine(filepath.Join(s.Dir, name), func(b []byte) {
			var v Sample
			if json.Unmarshal(b, &v) != nil || v.Time.IsZero() || !valid(v.InputVolts, 0, 1000) || !valid(v.OutputVolts, 0, 1000) || !valid(v.LoadPercent, 0, 100) || !valid(v.BatteryPercent, 0, 100) || !valid(v.RuntimeMinutes, 0, 100000) {
				return
			}
			slot := v.Time.UTC().Truncate(Interval)
			if slot.Before(start) || slot.After(end) {
				return
			}
			idx := int(slot.Sub(start) / Interval)
			// Keep first measured observation in the bucket; do not average through gaps.
			if result.Points[idx].Observed {
				return
			}
			result.Points[idx].InputVolts = v.InputVolts
			result.Points[idx].OutputVolts = v.OutputVolts
			result.Points[idx].LoadPercent = v.LoadPercent
			result.Points[idx].BatteryPercent = v.BatteryPercent
			result.Points[idx].RuntimeMinutes = v.RuntimeMinutes
			result.Points[idx].Power = v.Power
			result.Points[idx].Observed = true
			result.Samples++
		})
		if err != nil {
			return Result{}, err
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
			case "power_lost", "power_restored", "on_battery_detected", "ups_communication_lost", "ups_communication_restored", "low_battery", "battery_fully_charged":
				result.Events = append(result.Events, ev)
				// Event measurements are genuine readings recorded at the transition.
				// Backfill only otherwise missing battery/runtime fields in that
				// quarter-hour slot; never interpolate missing readings or power state.
				idx := int(ev.Time.UTC().Truncate(Interval).Sub(start) / Interval)
				if idx >= 0 && idx < len(result.Points) {
					bucket := &result.Points[idx]
					if bucket.BatteryPercent == nil {
						if n, ok := eventNumber(ev.Details, "battery_percent", 0, 100); ok { bucket.BatteryPercent = &n; bucket.BatteryFromEvent = true }
					}
					if bucket.RuntimeMinutes == nil {
						if n, ok := eventNumber(ev.Details, "runtime_minutes", 0, 100000); ok { bucket.RuntimeMinutes = &n; bucket.RuntimeFromEvent = true }
					}
				}
			}
		}
	}
	return result, nil
}

// eventNumber accepts only a valid numeric measurement actually stored in an event.
func eventNumber(details map[string]any, key string, min, max float64) (float64, bool) {
 v,ok:=details[key]; if !ok {return 0,false}
 n,ok:=v.(float64); if !ok || math.IsNaN(n) || math.IsInf(n,0) || n<min || n>max { return 0,false }
 return n,true
}
