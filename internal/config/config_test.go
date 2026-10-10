package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	defaultValue := Settings{PollIntervalSeconds: 10, LowBatteryPercent: 20, RetentionDays: 365}
	var applied Settings
	store, err := Open(path, defaultValue, func(s Settings) { applied = s })
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Get(); got != defaultValue {
		t.Fatalf("default: %+v", got)
	}
	changed := Settings{PollIntervalSeconds: 30, LowBatteryPercent: 15.5, RetentionDays: 0}
	if err := store.Save(changed); err != nil {
		t.Fatal(err)
	}
	if applied != changed {
		t.Fatal("change notification not called")
	}
	file, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Mode().Perm() != 0600 {
		t.Fatal("settings permissions aren't private")
	}
	reopened, err := Open(path, defaultValue, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Get() != changed {
		t.Fatalf("persisted value: %+v", reopened.Get())
	}
	if err := store.Save(Settings{PollIntervalSeconds: 1, LowBatteryPercent: 20, RetentionDays: 365}); err == nil {
		t.Fatal("invalid interval accepted")
	}
	if store.Get() != changed {
		t.Fatal("failed update mutated live settings")
	}
}
