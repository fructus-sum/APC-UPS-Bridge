package apc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseOnline(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	s, err := Parse(loadFixture(t, "online.txt"), now)
	if err != nil {
		t.Fatal(err)
	}
	if s.Power != Online || s.Name != "Old APC in rack" || s.Model != "Back-UPS RS 1500" {
		t.Fatalf("unexpected reading: %+v", s)
	}
	if s.BatteryPercent == nil || *s.BatteryPercent != 100 || s.RuntimeMinutes == nil || *s.RuntimeMinutes != 42.3 {
		t.Fatalf("metrics: %+v", s)
	}
}
func TestMultipleFlagsAndMissingFields(t *testing.T) {
	s, err := Parse(loadFixture(t, "boost_online.txt"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s.Power != Online || s.BatteryPercent != nil || s.RuntimeMinutes != nil {
		t.Fatalf("unexpected reading: %+v", s)
	}
}
func TestOnBatteryLow(t *testing.T) {
	s, err := Parse(loadFixture(t, "onbattery.txt"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s.Power != OnBattery || len(s.Flags) != 2 {
		t.Fatalf("unexpected reading: %+v", s)
	}
}
func TestCommLostAndContradiction(t *testing.T) {
	for _, v := range []string{"COMMLOST", "ONLINE ONBATT", "SHUTTING DOWN", "NOBATT ONLINE"} {
		s, err := Parse([]byte("STATUS : "+v+"\n"), time.Now())
		if err != nil || s.Power != Unknown {
			t.Errorf("%s -> %v, %v", v, s.Power, err)
		}
	}
}
func TestMissingStatus(t *testing.T) {
	if _, err := Parse(loadFixture(t, "no_status.txt"), time.Now()); err == nil {
		t.Fatal("missing status accepted")
	}
}
func TestMetricSafety(t *testing.T) {
	for _, v := range []string{"N/A", "NaN", "Inf", "-1 Percent", "101 Percent", "?"} {
		s, err := Parse([]byte("STATUS : ONLINE\nBCHARGE : "+v+"\n"), time.Now())
		if err != nil || s.BatteryPercent != nil {
			t.Errorf("%q: %+v, %v", v, s, err)
		}
	}
	s, err := Parse([]byte("STATUS : ONLINE\nBCHARGE : 020.0 Percent\n"), time.Now())
	if err != nil || s.BatteryPercent == nil || *s.BatteryPercent != 20 {
		t.Fatal(s, err)
	}
}
func TestMissingDelimitersIgnored(t *testing.T) {
	s, err := Parse([]byte("garbage\nSTATUS: ONLINE\n"), time.Now())
	if err != nil || s.Power != Online {
		t.Fatal(s, err)
	}
}
func TestLongLine(t *testing.T) {
	_, err := Parse([]byte("STATUS : ONLINE\nMODEL : "+strings.Repeat("x", 1024*1024+1)), time.Now())
	if err == nil {
		t.Fatal("expected oversize line to fail")
	}
}
