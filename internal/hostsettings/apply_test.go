package hostsettings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestReplaceThresholds(t *testing.T) {
	input := []byte("# keep comment\nBATTERYLEVEL 5\nMINUTES 2\nTIMEOUT 120\nNETSERVER on\n")
	out := string(replaceThresholds(input, Settings{Managed: true, BatteryPercent: 12, RuntimeMinutes: 4, TimeoutMinutes: 10}))
	for _, expected := range []string{"BATTERYLEVEL 12\n", "MINUTES 4\n", "TIMEOUT 600\n", "NETSERVER on\n", "# keep comment\n"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("missing %q in %q", expected, out)
		}
	}
	if off := string(replaceThresholds(input, Settings{Managed: true, BatteryPercent: 12, RuntimeMinutes: 4})); !strings.Contains(off, "TIMEOUT 0\n") {
		t.Fatalf("timer not disabled in %q", off)
	}
	if strings.Count(out, "BATTERYLEVEL") != 1 {
		t.Fatal("duplicate battery key")
	}
}
func TestApplyConfigBackupAndRollback(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "apcupsd.conf")
	original := []byte("BATTERYLEVEL 5\nMINUTES 2\nTIMEOUT 0\n")
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	settings := Settings{Managed: true, BatteryPercent: 15, RuntimeMinutes: 5}
	calls := 0
	if err := applyConfig(file, settings, func() error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	if !strings.Contains(string(got), "BATTERYLEVEL 15") {
		t.Fatalf("%s", got)
	}
	matches, _ := filepath.Glob(file + ".bridge-backup-*")
	if len(matches) != 1 {
		t.Fatalf("expected a backup, got %v", matches)
	}
	b, _ := os.ReadFile(matches[0])
	if string(b) != string(original) {
		t.Fatal("backup mismatch")
	}
	if calls != 1 {
		t.Fatalf("restart called %d times", calls)
	}
}

func TestApplyRestoresOnServiceRestartFailure(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "apcupsd.conf")
	old := []byte("BATTERYLEVEL 5\nMINUTES 2\nTIMEOUT 0\n")
	if err := os.WriteFile(file, old, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := applyConfig(file, Settings{Managed: true, BatteryPercent: 15, RuntimeMinutes: 4}, func() error {
		calls++
		if calls == 1 {
			return errors.New("restart failed")
		}
		return nil
	})
	if err == nil {
		t.Fatal("restart failure was ignored")
	}
	got, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	if string(got) != string(old) {
		t.Fatalf("configuration not restored: %s", got)
	}
	if calls != 2 {
		t.Fatalf("expected restart and rollback restart, got %d", calls)
	}
}

func TestApplyReportsFailedRollbackRestart(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "apcupsd.conf")
	if err := os.WriteFile(file, []byte("BATTERYLEVEL 5\nMINUTES 2\nTIMEOUT 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := applyConfig(file, Settings{Managed: true, BatteryPercent: 15, RuntimeMinutes: 4}, func() error { return errors.New("restart failed") })
	if err == nil || !strings.Contains(err.Error(), "may be stopped") {
		t.Fatalf("unexpected error: %v", err)
	}
}
func TestApplyConfigKeepsFiveBackups(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "apcupsd.conf")
	if err := os.WriteFile(file, []byte("BATTERYLEVEL 5\nMINUTES 2\nTIMEOUT 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 7; i++ {
		if err := os.WriteFile(fmt.Sprintf("%s.bridge-backup-2020010%dT000000.000000000", file, i), []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyConfig(file, Settings{Managed: true, BatteryPercent: 15, RuntimeMinutes: 4}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(file + ".bridge-backup-*")
	if len(matches) != 5 {
		t.Fatalf("expected 5 backups, got %v", matches)
	}
	sort.Strings(matches)
	b, _ := os.ReadFile(matches[len(matches)-1])
	if string(b) != "BATTERYLEVEL 5\nMINUTES 2\nTIMEOUT 0\n" {
		t.Fatalf("newest backup missing: %v", matches)
	}
}
func TestOnlineStatus(t *testing.T) {
	for _, c := range []struct {
		out string
		ok  bool
	}{{"APC : 001\nSTATUS   : ONLINE \n", true}, {"STATUS : ONBATT\n", false}, {"STATUS : ONLINE LOWBATT\n", false}, {"UPSNAME : x\n", false}, {"", false}} {
		if err := onlineStatus([]byte(c.out)); (err == nil) != c.ok {
			t.Fatalf("%q: ok=%v err=%v", c.out, c.ok, err)
		}
	}
}
