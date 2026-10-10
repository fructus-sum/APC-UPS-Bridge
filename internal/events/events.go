// Package events writes significant UPS events as monthly JSON Lines files.
// Normal successful status polling never writes to disk.
package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Event struct {
	Time    time.Time      `json:"time"`
	Type    string         `json:"type"`
	Level   string         `json:"level"`
	Details map[string]any `json:"details,omitempty"`
}

type Writer interface{ Write(Event) error }

type FileWriter struct{ Dir string }

func (w FileWriter) Write(e Event) error {
	if w.Dir == "" {
		return fmt.Errorf("log directory is empty")
	}
	if e.Type == "" {
		return fmt.Errorf("event type is empty")
	}
	if e.Time.IsZero() {
		return fmt.Errorf("event time is empty")
	}
	if err := os.MkdirAll(w.Dir, 0700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	name := "events-" + e.Time.UTC().Format("2006-01") + ".jsonl"
	path := filepath.Join(w.Dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	defer file.Close()
	e.Time = e.Time.UTC()
	encoded, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := file.Write(encoded); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	return nil
}

// Cleanup removes only completed monthly files whose entire month precedes
// the retention cutoff. 0 retains events forever. It ignores unknown files.
// Keeping a whole partially expired month errs on the side of preserving data.
func Cleanup(dir string, retentionDays int, now time.Time) (int, error) {
	if retentionDays < 0 {
		return 0, fmt.Errorf("retention days must be nonnegative")
	}
	if retentionDays == 0 {
		return 0, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cutoff := now.UTC().AddDate(0, 0, -retentionDays)
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "events-") || !strings.HasSuffix(name, ".jsonl") || len(name) != len("events-2006-01.jsonl") {
			continue
		}
		month, err := time.Parse("2006-01", name[len("events-"):len(name)-len(".jsonl")])
		if err != nil {
			continue
		}
		endOfMonth := month.AddDate(0, 1, 0)
		if endOfMonth.After(cutoff) {
			continue
		}
		// Don't follow symlinks. There is no need to touch foreign files.
		if !entry.Type().IsRegular() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
