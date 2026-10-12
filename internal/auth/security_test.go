package auth

import (
	"errors"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"
)

func TestFifthFailureSignalsAttackAndResets(t *testing.T) {
	path := t.TempDir() + "/auth.json"
	if err := Initialize(path, "a long and unique secret"); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		if _, _, err := s.Login("192.0.2.10", "admin", "invalid"); err != ErrCredentials {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	if _, _, err := s.Login("192.0.2.10", "admin", "invalid"); err != ErrSuspicious {
		t.Fatalf("fifth attempt: %v", err)
	}
	// An authorized login clears failures before the sixth attempt.
	if _, _, err := s.Login("192.0.2.10", "admin", "a long and unique secret"); err != nil {
		t.Fatalf("correct password rejected: %v", err)
	}
	if _, _, err := s.Login("192.0.2.10", "admin", "invalid"); err != ErrCredentials {
		t.Fatalf("failures not reset: %v", err)
	}
	now = now.Add(11 * time.Minute)
	if _, _, err := s.Login("192.0.2.10", "admin", "invalid"); err != ErrCredentials {
		t.Fatalf("time window not expired: %v", err)
	}
}

func TestSuspiciousIsCredentialFailure(t *testing.T) {
	if !errors.Is(ErrSuspicious, ErrCredentials) {
		t.Fatal("ErrSuspicious must count as a credential failure")
	}
}

func TestThresholdsMatchFail2banJail(t *testing.T) {
	script, err := os.ReadFile("../../scripts/setup-fail2ban.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	retry := regexp.MustCompile(`(?m)^maxretry = (\d+)$`).FindStringSubmatch(text)
	window := regexp.MustCompile(`(?m)^findtime = (\d+)m$`).FindStringSubmatch(text)
	if retry == nil || window == nil {
		t.Fatal("jail maxretry/findtime not found in scripts/setup-fail2ban.sh")
	}
	if n, _ := strconv.Atoi(retry[1]); n != suspiciousFailures {
		t.Fatalf("jail maxretry %d != suspiciousFailures %d", n, suspiciousFailures)
	}
	if n, _ := strconv.Atoi(window[1]); time.Duration(n)*time.Minute != attemptWindow {
		t.Fatalf("jail findtime %dm != attemptWindow %v", n, attemptWindow)
	}
}
