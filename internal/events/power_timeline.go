package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MaxPowerTimelineEvents bounds memory and response size independently of the
// general-purpose 500-event report API. Power transitions are much rarer than
// other event types and should not disappear behind unrelated log entries.
const MaxPowerTimelineEvents = 8192

// ReadPowerTimeline returns only recorded UPS power / charge events inside the
// requested window, oldest first. Unlike ReadHistory, it does not truncate the
// earlier part of a long window because of unrelated event types.
func ReadPowerTimeline(dir string, from, to time.Time) ([]Event, error) {
	if from.After(to) || to.Sub(from) > 366*24*time.Hour {
		return nil, fmt.Errorf("invalid power timeline range")
	}
	result := make([]Event, 0)
	if dir == "" {
		return result, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, 13)
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || len(name) != len("events-2006-01.jsonl") ||
			!strings.HasPrefix(name, "events-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		month, parseErr := time.Parse("2006-01", strings.TrimSuffix(strings.TrimPrefix(name, "events-"), ".jsonl"))
		if parseErr != nil || month.After(to) || !month.AddDate(0, 1, 0).After(from) {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)
	for _, name := range files {
		f, openErr := os.Open(filepath.Join(dir, name))
		if openErr != nil {
			return nil, openErr
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 4096), 1024*1024)
		for scan.Scan() {
			var ev Event
			if json.Unmarshal(scan.Bytes(), &ev) != nil || ev.Time.Before(from) || ev.Time.After(to) {
				continue
			}
			switch ev.Type {
			case "power_lost", "on_battery_detected", "power_restored", "low_battery",
				"ups_communication_lost", "ups_communication_restored", "battery_fully_charged":
				if len(result) == MaxPowerTimelineEvents {
					_ = f.Close()
					return nil, fmt.Errorf("too many power events in requested period (limit %d)", MaxPowerTimelineEvents)
				}
				result = append(result, ev)
			}
		}
		readErr := scan.Err()
		closeErr := f.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", name, readErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Time.Before(result[j].Time) })
	return result, nil
}
