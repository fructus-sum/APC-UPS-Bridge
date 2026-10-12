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
	Time           time.Time      `json:"time"`
	InputVolts     *float64       `json:"input_volts"`
	OutputVolts    *float64       `json:"output_volts"`
	LoadPercent    *float64       `json:"load_percent"`
	BatteryPercent *float64       `json:"battery_percent,omitempty"`
	RuntimeMinutes *float64       `json:"runtime_minutes,omitempty"`
	Power          apc.PowerState `json:"power,omitempty"`
}

type Bucket struct {
	Time             time.Time      `json:"time"`
	InputVolts       *float64       `json:"input_volts"`
	OutputVolts      *float64       `json:"output_volts"`
	LoadPercent      *float64       `json:"load_percent"`
	Observed         bool           `json:"observed"`
	BatteryPercent   *float64       `json:"battery_percent,omitempty"`
	RuntimeMinutes   *float64       `json:"runtime_minutes,omitempty"`
	Power            apc.PowerState `json:"power,omitempty"`
	BatteryFromEvent bool           `json:"battery_from_event,omitempty"`
	RuntimeFromEvent bool           `json:"runtime_from_event,omitempty"`
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
	// A bad battery or runtime value only drops that field; it must not
	// discard the voltage and load readings taken at the same time.
	battery := validOrNil(snap.BatteryPercent, 0, 100)
	runtime := validOrNil(snap.RuntimeMinutes, 0, 100000)
	if snap.InputVolts == nil && snap.OutputVolts == nil && snap.LoadPercent == nil && battery == nil && runtime == nil {
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
	rec := Sample{Time: now, InputVolts: clone(snap.InputVolts), OutputVolts: clone(snap.OutputVolts), LoadPercent: clone(snap.LoadPercent), BatteryPercent: clone(battery), RuntimeMinutes: clone(runtime), Power: snap.Power}
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

// validOrNil returns p when it is absent or within range, otherwise nil.
func validOrNil(p *float64, min, max float64) *float64 {
	if valid(p, min, max) {
		return p
	}
	return nil
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
			if json.Unmarshal(b, &v) != nil || v.Time.IsZero() || !valid(v.InputVolts, 0, 1000) || !valid(v.OutputVolts, 0, 1000) || !valid(v.LoadPercent, 0, 100) {
				return
			}
			v.BatteryPercent = validOrNil(v.BatteryPercent, 0, 100)
			v.RuntimeMinutes = validOrNil(v.RuntimeMinutes, 0, 100000)
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
		hist, err := events.ReadPowerTimeline(eventDir, start, now)
		if err != nil {
			return Result{}, err
		}
		result.Events = hist
		backfillFromEvents(result.Points, start, hist)
	}
	return result, nil
}

// backfillFromEvents copies battery/runtime readings recorded in events into
// the grid slot of the event. Event measurements are genuine readings taken at
// the transition. Only otherwise missing fields are filled; readings and power
// state are never interpolated. Events outside the grid are ignored.
func backfillFromEvents(points []Bucket, gridStart time.Time, evs []events.Event) {
	for _, ev := range evs {
		if ev.Time.Before(gridStart) {
			continue
		}
		idx := int(ev.Time.UTC().Truncate(Interval).Sub(gridStart) / Interval)
		if idx < 0 || idx >= len(points) {
			continue
		}
		p := &points[idx]
		if p.BatteryPercent == nil {
			if n, ok := eventNumber(ev.Details, "battery_percent", 0, 100); ok {
				p.BatteryPercent = &n
				p.BatteryFromEvent = true
			}
		}
		if p.RuntimeMinutes == nil {
			if n, ok := eventNumber(ev.Details, "runtime_minutes", 0, 100000); ok {
				p.RuntimeMinutes = &n
				p.RuntimeFromEvent = true
			}
		}
	}
}

const DefaultBatteryDays = 30

// ReadBattery returns the independently selectable battery-only graph history.
// It contains at most 30 days of actual 15-minute samples (the existing
// retention policy), plus true event timestamps across the requested year.
// The sparse older section does not invent 15-minute samples or uptime.
// The returned range ends at now, so events in the current quarter-hour are
// inside it.
func (s *Store) ReadBattery(days int, now time.Time, eventDir string) (Result, error) {
	switch days {
	case 30, 90, 180, 365:
	default:
		return Result{}, fmt.Errorf("battery range must be 30, 90, 180 or 365 days")
	}
	now = now.UTC()
	start := now.Truncate(Interval).Add(-time.Duration(days) * 24 * time.Hour)
	recent, err := s.Read(30, now, "")
	if err != nil {
		return Result{}, err
	}
	result := Result{
		From: start, To: now, IntervalSeconds: int(Interval.Seconds()),
		Points: recent.Points, Events: []events.Event{}, Samples: recent.Samples,
	}
	if eventDir == "" {
		return result, nil
	}
	all, err := events.ReadPowerTimeline(eventDir, start, now)
	if err != nil {
		return Result{}, err
	}
	result.Events = all
	// Only backfill the recent grid. Older events remain isolated dots with
	// their exact timestamps and must never extend the recent sample lines.
	backfillFromEvents(result.Points, recent.From, all)
	return result, nil
}

// eventNumber accepts only a valid numeric measurement actually stored in an event.
func eventNumber(details map[string]any, key string, min, max float64) (float64, bool) {
	v, ok := details[key]
	if !ok {
		return 0, false
	}
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < min || n > max {
		return 0, false
	}
	return n, true
}
