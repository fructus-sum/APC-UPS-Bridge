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
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	files := make([]string, 0)
	for _, item := range items {
		name := item.Name()
		if !item.Type().IsRegular() || !strings.HasPrefix(name, "events-") || !strings.HasSuffix(name, ".jsonl") || len(name) != len("events-2006-01.jsonl") {
			continue
		}
		if _, err = time.Parse("2006-01", strings.TrimSuffix(strings.TrimPrefix(name, "events-"), ".jsonl")); err == nil {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	cutoff := time.Time{}
	if days > 0 {
		cutoff = now.UTC().AddDate(0, 0, -days)
	}
	for _, name := range files {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return result, err
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 4096), 1024*1024)
		for scan.Scan() {
			var ev Event
			if json.Unmarshal(scan.Bytes(), &ev) != nil || ev.Type == "" || ev.Time.IsZero() || (!cutoff.IsZero() && ev.Time.Before(cutoff)) {
				continue
			}
			switch ev.Type {
			case "power_lost", "on_battery_detected":
				result.Report.Outages++
			case "power_restored":
				result.Report.Restorations++
				if sec, ok := ev.Details["observed_outage_seconds"].(float64); ok && sec >= 0 && sec < 1e9 {
					result.Report.ObservedOutageSeconds += int64(sec)
					if int64(sec) > result.Report.LongestObservedOutageSeconds {
						result.Report.LongestObservedOutageSeconds = int64(sec)
					}
				}
			case "low_battery":
				result.Report.LowBatteryWarnings++
			case "ups_communication_lost":
				result.Report.CommunicationFailures++
			}
			// Retain the bounded newest set, even if log entries were out of order.
			result.Events = append(result.Events, ev)
			if len(result.Events) > limit*2 {
				sort.Slice(result.Events, func(i, j int) bool { return result.Events[i].Time.After(result.Events[j].Time) })
				result.Events = result.Events[:limit]
			}
		}
		scanErr := scan.Err()
		closeErr := f.Close()
		if scanErr != nil {
			return result, fmt.Errorf("scan %s: %w", name, scanErr)
		}
		if closeErr != nil {
			return result, closeErr
		}
	}
	sort.Slice(result.Events, func(i, j int) bool { return result.Events[i].Time.After(result.Events[j].Time) })
	if len(result.Events) > limit {
		result.Events = result.Events[:limit]
	}
	return result, nil
}
