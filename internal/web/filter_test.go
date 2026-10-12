package web

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

const journalPrefix = "Oct 12 10:00:00 pi apc-ups-bridge[123]: "

// A failed login must write exactly the line the Fail2ban filter expects.
func TestFailedLoginLogLineFormat(t *testing.T) {
	h := newFixture(t).Handler
	var out bytes.Buffer
	log.SetOutput(&out)
	defer log.SetOutput(os.Stderr)
	body := url.Values{"username": {"admin"}, "password": {"wrong password"}}.Encode()
	for _, remote := range []string{"192.0.2.1:5555", "[2001:db8::1]:5555"} {
		out.Reset()
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = remote
		h.ServeHTTP(httptest.NewRecorder(), r)
		ip := strings.TrimSuffix(strings.TrimPrefix(remote[:strings.LastIndex(remote, ":")], "["), "]")
		want := regexp.MustCompile(`^\d{4}/\d\d/\d\d \d\d:\d\d:\d\d ` + regexp.QuoteMeta(fmt.Sprintf(authFailureLogFormat, ip)) + "\n$")
		if !want.MatchString(out.String()) {
			t.Fatalf("unexpected log output for %s: %q", remote, out.String())
		}
	}
}

// The shipped filter must match journal lines in the real log format.
func TestFail2banFilterMatchesLogLine(t *testing.T) {
	raw, err := os.ReadFile("../../packaging/fail2ban/filter.d/apc-ups-bridge.conf")
	if err != nil {
		t.Fatal(err)
	}
	var failregex string
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "failregex"); ok {
			failregex = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "="))
		}
	}
	if !strings.Contains(failregex, "<HOST>") {
		t.Fatalf("filter has no usable failregex: %q", failregex)
	}
	re, err := regexp.Compile(strings.ReplaceAll(failregex, "<HOST>", `(?:(?:\d{1,3}\.){3}\d{1,3}|[0-9a-fA-F:]+)`))
	if err != nil {
		t.Fatal(err)
	}
	line := func(ip string) string {
		return journalPrefix + "2026/10/12 10:00:00 " + fmt.Sprintf(authFailureLogFormat, ip)
	}
	for _, ip := range []string{"192.0.2.1", "2001:db8::1"} {
		if !re.MatchString(line(ip)) {
			t.Errorf("filter does not match %q", line(ip))
		}
		if re.MatchString(line(ip) + " extra") {
			t.Errorf("filter matches line with trailing text for %s", ip)
		}
	}
}
