// Package config stores the small set of editable monitoring preferences.
// Writes are atomic; there is no configuration database.
package config

import (
	"fmt"
	"sync"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/fileutil"
)

// MaxPollIntervalSeconds bounds the polling interval: shutdown decisions are
// made only at poll time, so a long interval could miss a whole battery window.
const MaxPollIntervalSeconds = 60

type Settings struct {
	PollIntervalSeconds int     `json:"poll_interval_seconds"`
	LowBatteryPercent   float64 `json:"low_battery_percent"`
	RetentionDays       int     `json:"retention_days"`
}

func (s Settings) Validate() error {
	if s.PollIntervalSeconds < 5 || s.PollIntervalSeconds > MaxPollIntervalSeconds {
		return fmt.Errorf("poll interval must be 5–%d seconds", MaxPollIntervalSeconds)
	}
	if s.LowBatteryPercent < 0 || s.LowBatteryPercent > 100 {
		return fmt.Errorf("battery warning threshold must be between 0 and 100")
	}
	if s.RetentionDays < 0 || s.RetentionDays > 36500 {
		return fmt.Errorf("retention must be 0 (forever) or 1–36500 days")
	}
	return nil
}

type Store struct {
	mu     sync.RWMutex
	path   string
	value  Settings
	notify func(Settings)
}

func Open(path string, defaults Settings, notify func(Settings)) (*Store, error) {
	if err := defaults.Validate(); err != nil {
		return nil, err
	}
	s := &Store{path: path, value: defaults, notify: notify}
	var v Settings
	found, err := fileutil.ReadJSON(path, &v)
	if !found && err == nil {
		return s, nil
	}
	if err != nil {
		if found {
			err = fmt.Errorf("invalid preferences: %w", err)
		}
		return nil, err
	}
	// Older settings files allowed up to 3600 seconds; clamp rather than refuse to start.
	if v.PollIntervalSeconds > MaxPollIntervalSeconds {
		v.PollIntervalSeconds = MaxPollIntervalSeconds
	}
	if err = v.Validate(); err != nil {
		return nil, fmt.Errorf("invalid preferences: %w", err)
	}
	s.value = v
	return s, nil
}
func (s *Store) Get() Settings { s.mu.RLock(); defer s.mu.RUnlock(); return s.value }
func (s *Store) Save(v Settings) error {
	if err := v.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fileutil.WriteJSONAtomic(s.path, v, 0600); err != nil {
		return err
	}
	s.value = v
	if s.notify != nil {
		s.notify(v)
	}
	return nil
}
