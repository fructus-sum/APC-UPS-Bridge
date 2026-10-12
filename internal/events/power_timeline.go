package events

import (
	"fmt"
	"sort"
	"time"
)

// maxPowerTimelineEvents bounds memory and response size independently of the
// general-purpose 500-event report API. Power transitions are much rarer than
// other event types and should not disappear behind unrelated log entries.
const maxPowerTimelineEvents = 8192

// powerTimelineTypes is the single list of event types shown on the history
// graphs.
var powerTimelineTypes = map[string]bool{
	TypePowerLost: true, TypeOnBatteryDetected: true, TypePowerRestored: true, TypeLowBattery: true,
	TypeUPSCommunicationLost: true, TypeUPSCommunicationRestored: true, TypeBatteryFullyCharged: true,
}

// newestOldestFirst sorts events oldest first and keeps only the newest max.
func newestOldestFirst(list []Event, max int) []Event {
	sort.SliceStable(list, func(i, j int) bool { return list[i].Time.Before(list[j].Time) })
	if len(list) > max {
		list = append(list[:0], list[len(list)-max:]...)
	}
	return list
}

// ReadPowerTimeline returns only recorded UPS power / charge events inside the
// requested window, oldest first. Unlike ReadHistory, it does not truncate the
// earlier part of a long window because of unrelated event types. If more than
// maxPowerTimelineEvents match, the oldest are dropped and the newest
// maxPowerTimelineEvents are returned without an error.
func ReadPowerTimeline(dir string, from, to time.Time) ([]Event, error) {
	return readPowerTimeline(dir, from, to, maxPowerTimelineEvents)
}

// readPowerTimeline is ReadPowerTimeline with an adjustable cap, so tests can
// exercise the cap without writing thousands of events.
func readPowerTimeline(dir string, from, to time.Time, max int) ([]Event, error) {
	if from.After(to) || to.Sub(from) > 366*24*time.Hour {
		return nil, fmt.Errorf("invalid power timeline range")
	}
	result := make([]Event, 0)
	if dir == "" {
		return result, nil
	}
	err := eachEventInWindow(dir, from, to, func(ev Event) {
		if !powerTimelineTypes[ev.Type] {
			return
		}
		result = append(result, ev)
		if len(result) >= 2*max {
			result = newestOldestFirst(result, max)
		}
	})
	if err != nil {
		return nil, err
	}
	return newestOldestFirst(result, max), nil
}
