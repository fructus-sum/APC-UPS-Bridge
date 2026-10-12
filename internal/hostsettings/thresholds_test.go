package hostsettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadThresholds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apcupsd.conf")
	conf := "# comment\n\nbatterylevel 15\nMINUTES -3\nTIMEOUT 600 extra\nMINUTES 7\nUPSNAME x\nBATTERYLEVEL\n"
	if err := os.WriteFile(path, []byte(conf), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadThresholds(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.BatteryPercent == nil || *got.BatteryPercent != 15 ||
		got.RuntimeMinutes == nil || *got.RuntimeMinutes != 7 ||
		got.TimeoutSeconds == nil || *got.TimeoutSeconds != 600 {
		t.Fatalf("unexpected thresholds: %+v", got)
	}
}

func TestReadThresholdsMissingFileAndUnsetValues(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadThresholds(filepath.Join(dir, "missing"))
	if err == nil || got != (Thresholds{}) {
		t.Fatalf("missing file: %+v, %v", got, err)
	}
	path := filepath.Join(dir, "empty.conf")
	os.WriteFile(path, []byte("TIMEOUT abc\n"), 0600)
	got, err = ReadThresholds(path)
	if err != nil || got != (Thresholds{}) {
		t.Fatalf("unset values: %+v, %v", got, err)
	}
}
