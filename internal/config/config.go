// Package config stores the small set of editable monitoring preferences.
// Writes are atomic; there is no configuration database.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	PollIntervalSeconds int     `json:"poll_interval_seconds"`
	LowBatteryPercent   float64 `json:"low_battery_percent"`
	RetentionDays       int     `json:"retention_days"`
}

func (s Settings) Validate() error {
	if s.PollIntervalSeconds < 5 || s.PollIntervalSeconds > 3600 {
		return fmt.Errorf("poll interval must be 5–3600 seconds")
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
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var v Settings
	if err = json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("invalid preferences: %w", err)
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
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = atomicWrite(s.path, append(data, '\n')); err != nil {
		return err
	}
	s.value = v
	if s.notify != nil {
		s.notify(v)
	}
	return nil
}
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".prefs-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
