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

// Report is computed on demand from event-only files. A missing duration is not guessed.
type Report struct {
	Outages                      int   `json:"outages"`
	Restorations                 int   `json:"restorations"`
	LowBatteryWarnings           int   `json:"low_battery_warnings"`
	CommunicationFailures        int   `json:"communication_failures"`
	ObservedOutageSeconds        int64 `json:"observed_outage_seconds"`
	LongestObservedOutageSeconds int64 `json:"longest_observed_outage_seconds"`
}
type History struct {
	Events []Event `json:"events"`
	Report Report  `json:"report"`
}

// ReadHistory returns the latest events (newest first) and aggregate counts.
// days=0 includes all retained events; invalid lines are ignored, not displayed.
func ReadHistory(dir string, days, limit int, now time.Time) (History, error) {
	if days < 0 || days > 36500 || limit < 1 || limit > 500 {
		return History{}, fmt.Errorf("invalid history request")
	}
	result := History{Events: []Event{}}
	cutoff := time.Time{}
	if days > 0 {
		cutoff = now.UTC().AddDate(0, 0, -days)
	}
	err := eachEventInWindow(dir, cutoff, time.Time{}, func(ev Event) {
		if ev.Type == "" || ev.Time.IsZero() {
			return
		}
		switch ev.Type {
		case TypePowerLost, TypeOnBatteryDetected:
			result.Report.Outages++
		case TypePowerRestored:
			result.Report.Restorations++
			if sec, ok := ev.Details[DetailObservedOutageSeconds].(float64); ok && sec >= 0 && sec < 1e9 {
				result.Report.ObservedOutageSeconds += int64(sec)
				if int64(sec) > result.Report.LongestObservedOutageSeconds {
					result.Report.LongestObservedOutageSeconds = int64(sec)
				}
			}
		case TypeLowBattery:
			result.Report.LowBatteryWarnings++
		case TypeUPSCommunicationLost:
			result.Report.CommunicationFailures++
		}
		// Retain the bounded newest set, even if log entries were out of order.
		result.Events = append(result.Events, ev)
		if len(result.Events) > limit*2 {
			sort.Slice(result.Events, func(i, j int) bool { return result.Events[i].Time.After(result.Events[j].Time) })
			result.Events = result.Events[:limit]
		}
	})
	if err != nil {
		return result, err
	}
	sort.Slice(result.Events, func(i, j int) bool { return result.Events[i].Time.After(result.Events[j].Time) })
	if len(result.Events) > limit {
		result.Events = result.Events[:limit]
	}
	return result, nil
}

// eventFileMonth returns the month of an events-YYYY-MM.jsonl file name.
func eventFileMonth(name string) (time.Time, bool) {
	if len(name) != len("events-2006-01.jsonl") || !strings.HasPrefix(name, "events-") || !strings.HasSuffix(name, ".jsonl") {
		return time.Time{}, false
	}
	month, err := time.Parse("2006-01", name[len("events-"):len(name)-len(".jsonl")])
	return month, err == nil
}

// eachEventInWindow calls fn, oldest file first and in file order, for every
// parsable event dated within [from, to]. A zero from or to leaves that side
// open. A missing directory is not an error; unparsable lines are ignored.
func eachEventInWindow(dir string, from, to time.Time, fn func(Event)) error {
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	files := make([]string, 0)
	for _, item := range items {
		month, ok := eventFileMonth(item.Name())
		if !ok || !item.Type().IsRegular() ||
			(!to.IsZero() && month.After(to)) || (!from.IsZero() && !month.AddDate(0, 1, 0).After(from)) {
			continue
		}
		files = append(files, item.Name())
	}
	sort.Strings(files)
	for _, name := range files {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 4096), 1024*1024)
		for scan.Scan() {
			var ev Event
			if json.Unmarshal(scan.Bytes(), &ev) != nil || ev.Time.Before(from) || (!to.IsZero() && ev.Time.After(to)) {
				continue
			}
			fn(ev)
		}
		scanErr := scan.Err()
		closeErr := f.Close()
		if scanErr != nil {
			return fmt.Errorf("read %s: %w", name, scanErr)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
