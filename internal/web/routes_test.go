package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/auth"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/config"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/history"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/notify"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

const routesPassphrase = "long secret passphrase"

// routeEnv is one fully wired handler plus a logged-in session.
type routeEnv struct {
	h      http.Handler
	cookie *http.Cookie
	csrf   string
	auth   *auth.Store
}

// buildRouteEnv wires the handler the way the daemon does. Variant "bare"
// leaves out power, email and history; "offline" never polls the UPS.
func buildRouteEnv(t *testing.T, variant string) *routeEnv {
	t.Helper()
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	if err := auth.Initialize(authPath, routesPassphrase); err != nil {
		t.Fatal(err)
	}
	admin, err := auth.Open(authPath)
	if err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(dir, "logs")
	mon, err := monitor.New(testReader{}, events.FileWriter{Dir: logs}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if variant != "offline" {
		if err = mon.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	prefs, err := config.Open(filepath.Join(dir, "prefs.json"), config.Settings{PollIntervalSeconds: 10, LowBatteryPercent: 20, RetentionDays: 365}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var handler http.Handler
	if variant == "bare" {
		handler, err = New(mon, admin, prefs, logs)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		store, err := power.OpenConfig(filepath.Join(dir, "power.json"))
		if err != nil {
			t.Fatal(err)
		}
		ctrl, err := power.OpenController(store, filepath.Join(dir, "power-state.json"), events.FileWriter{Dir: logs}, inertExec{})
		if err != nil {
			t.Fatal(err)
		}
		handler, err = New(mon, admin, prefs, logs, ctrl)
		if err != nil {
			t.Fatal(err)
		}
		mstore, err := notify.OpenConfig(filepath.Join(dir, "email.json"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := notify.Config{Enabled: true, Host: "mail.example.net", Port: 465, Security: "tls", Username: "alert@example.net", Password: "secret", From: "alert@example.net", To: "owner@example.net", WaitForDevices: true}
		if err = mstore.Save(cfg); err != nil {
			t.Fatal(err)
		}
		engine, err := notify.Open(mstore, filepath.Join(dir, "state.json"), filepath.Join(dir, "events"), events.FileWriter{Dir: filepath.Join(dir, "events")}, mailSender{})
		if err != nil {
			t.Fatal(err)
		}
		AttachEmail(handler, engine)
		AttachHistory(handler, history.New(filepath.Join(dir, "history")))
	}
	token, csrf, err := admin.Login("127.0.0.1", "admin", routesPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	return &routeEnv{h: handler, cookie: &http.Cookie{Name: "ups_bridge_session", Value: token}, csrf: csrf, auth: admin}
}

type routeCase struct {
	name    string
	variant string // "", "bare" or "offline"
	method  string
	path    string
	body    string
	anon    bool   // send no session cookie
	noCSRF  bool   // logged in, but without the CSRF token
	code    int    // expected status
	want    string // exact text (without newline) of a plain-text error
	has     string // substring of the body
	ctype   string // expected Content-Type
	loc     string // expected redirect target
	fresh   bool   // use a private session (the request ends it)
}

func spaces(n int) string { return strings.Repeat(" ", n) }

func jsonString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEveryRoute(t *testing.T) {
	settings := jsonString(t, config.Settings{PollIntervalSeconds: 10, LowBatteryPercent: 20, RetentionDays: 365})
	badSettings := jsonString(t, config.Settings{})
	pc := power.Defaults()
	powerBody := jsonString(t, pc)
	pc.Devices = []power.Device{{ID: "x", Name: "X", Host: "-oProxyCommand=evil", SSHUser: "root", SSHPort: 22}}
	badPower := jsonString(t, pc)
	mailBody := jsonString(t, notify.Config{Enabled: true, Host: "mail.example.net", Port: 465, Security: "tls", Username: "alert@example.net", From: "alert@example.net", To: "owner@example.net"})
	badMail := jsonString(t, notify.Config{Enabled: true})
	hostOK := `{"managed":true,"battery_percent":10,"runtime_minutes":3}`

	const (
		html = "text/html; charset=utf-8"
		js   = "application/javascript; charset=utf-8"
		jsn  = "application/json; charset=utf-8"
	)
	post, get, put := http.MethodPost, http.MethodGet, http.MethodPut
	cases := []routeCase{
		// Not signed in.
		{name: "anon api", method: get, path: "/api/status", anon: true, code: 401, want: "authentication required"},
		{name: "anon api post", method: post, path: "/api/settings", body: settings, anon: true, code: 401, want: "authentication required"},
		{name: "anon unknown api", method: get, path: "/api/nope", anon: true, code: 401, want: "authentication required"},
		{name: "anon page", method: get, path: "/", anon: true, code: 303, loc: "/login"},
		{name: "anon unknown page", method: get, path: "/nope", anon: true, code: 303, loc: "/login"},
		{name: "anon app.js", method: get, path: "/assets/app.js", anon: true, code: 303, loc: "/login"},
		{name: "anon email.js", method: get, path: "/assets/email.js", anon: true, code: 303, loc: "/login"},
		{name: "anon history.js", method: get, path: "/assets/history.js", anon: true, code: 303, loc: "/login"},
		{name: "anon css post", method: post, path: "/assets/app.css", anon: true, code: 303, loc: "/login"},
		{name: "anon logout", method: post, path: "/logout", anon: true, code: 303, loc: "/login"},
		{name: "anon change-password", method: post, path: "/change-password", anon: true, code: 303, loc: "/login"},
		{name: "anon fail2ban", method: get, path: "/api/security/fail2ban", anon: true, code: 401, want: "authentication required"},
		{name: "anon email", method: get, path: "/email", anon: true, code: 303, loc: "/login"},

		// Public pages.
		{name: "css", method: get, path: "/assets/app.css", anon: true, code: 200, ctype: "text/css; charset=utf-8"},
		{name: "css signed in", method: get, path: "/assets/app.css", code: 200, ctype: "text/css; charset=utf-8"},
		{name: "css post signed in", method: post, path: "/assets/app.css", code: 404, want: "404 page not found"},
		{name: "login page", method: get, path: "/login", anon: true, code: 200, ctype: html, has: "<form"},
		{name: "login page changed", method: get, path: "/login?changed=1", anon: true, code: 200, ctype: html, has: "Password updated. Sign in again."},
		{name: "login wrong method", method: put, path: "/login", anon: true, code: 405, want: "method not allowed"},
		{name: "login bad credentials", method: post, path: "/login", body: "username=admin&password=wrong", anon: true, code: 401, ctype: html, has: "Invalid credentials or too many attempts. Try again later."},
		{name: "login oversize", method: post, path: "/login", body: "username=" + strings.Repeat("a", 9000), anon: true, code: 400, want: "bad request"},
		{name: "login ok", method: post, path: "/login", body: "username=admin&password=" + url.QueryEscape(routesPassphrase), anon: true, code: 303, loc: "/"},
		{name: "login signed in still serves login", method: get, path: "/login", code: 200, ctype: html},

		// Pages and scripts.
		{name: "dashboard", method: get, path: "/", code: 200, ctype: html},
		{name: "events page", method: get, path: "/events", code: 200, ctype: html},
		{name: "settings page", method: get, path: "/settings", code: 200, ctype: html},
		{name: "power page", method: get, path: "/power", code: 200, ctype: html},
		{name: "history page", method: get, path: "/history", code: 200, ctype: html},
		{name: "security page", method: get, path: "/security", code: 200, ctype: html},
		{name: "email page", method: get, path: "/email", code: 200, ctype: html},
		{name: "email page bare", variant: "bare", method: get, path: "/email", code: 200, ctype: html},
		{name: "dashboard post", method: post, path: "/", code: 405, want: "method not allowed"},
		{name: "events page post", method: post, path: "/events", code: 405, want: "method not allowed"},
		{name: "settings page put", method: put, path: "/settings", code: 405, want: "method not allowed"},
		{name: "power page post", method: post, path: "/power", code: 405, want: "method not allowed"},
		{name: "history page post", method: post, path: "/history", code: 405, want: "method not allowed"},
		{name: "security page post", method: post, path: "/security", code: 405, want: "method not allowed"},
		{name: "email page post", method: post, path: "/email", code: 405, want: "method not allowed"},
		{name: "unknown page", method: get, path: "/nope", code: 404, want: "404 page not found"},
		{name: "unknown api", method: get, path: "/api/nope", code: 404, want: "404 page not found"},
		{name: "app.js", method: get, path: "/assets/app.js", code: 200, ctype: js},
		{name: "email.js", method: get, path: "/assets/email.js", code: 200, ctype: js},
		{name: "history.js", method: get, path: "/assets/history.js", code: 200, ctype: js},
		{name: "app.js post", method: post, path: "/assets/app.js", code: 405, want: "method not allowed"},
		{name: "email.js post", method: post, path: "/assets/email.js", code: 405, want: "method not allowed"},
		{name: "history.js post", method: post, path: "/assets/history.js", code: 405, want: "method not allowed"},
		{name: "unknown asset", method: get, path: "/assets/nope.js", code: 404, want: "404 page not found"},

		// Status and firmware.
		{name: "status", method: get, path: "/api/status", code: 200, ctype: jsn, has: "91"},
		{name: "status post", method: post, path: "/api/status", code: 405, want: "method not allowed"},
		{name: "firmware", method: get, path: "/api/firmware", code: 200, ctype: jsn},
		{name: "firmware post", method: post, path: "/api/firmware", code: 405, want: "method not allowed"},

		// Events, report and CSV.
		{name: "events", method: get, path: "/api/events", code: 200, ctype: jsn},
		{name: "events query", method: get, path: "/api/events?days=0&limit=500", code: 200, ctype: jsn},
		{name: "report", method: get, path: "/api/report?days=36500&limit=1", code: 200, ctype: jsn},
		{name: "csv", method: get, path: "/api/events.csv", code: 200, ctype: "text/csv; charset=utf-8", has: "timestamp_utc,event,level,details_json"},
		{name: "events post", method: post, path: "/api/events", code: 405, want: "method not allowed"},
		{name: "csv post", method: post, path: "/api/events.csv", code: 405, want: "method not allowed"},
		{name: "report post", method: post, path: "/api/report", code: 405, want: "method not allowed"},
		{name: "events bad days", method: get, path: "/api/events?days=abc", code: 400, want: "invalid days"},
		{name: "events negative days", method: get, path: "/api/events?days=-1", code: 400, want: "invalid days"},
		{name: "events huge days", method: get, path: "/api/events?days=36501", code: 400, want: "invalid days"},
		{name: "csv bad limit", method: get, path: "/api/events.csv?limit=0", code: 400, want: "invalid limit"},
		{name: "report big limit", method: get, path: "/api/report?limit=501", code: 400, want: "invalid limit"},
		{name: "events bad days beats bad limit", method: get, path: "/api/events?days=x&limit=x", code: 400, want: "invalid days"},

		// Settings.
		{name: "settings get", method: get, path: "/api/settings", code: 200, ctype: jsn},
		{name: "settings put", method: put, path: "/api/settings", body: settings, code: 405, want: "method not allowed"},
		{name: "settings no csrf", method: post, path: "/api/settings", body: settings, noCSRF: true, code: 403, want: "invalid CSRF token"},
		{name: "settings ok", method: post, path: "/api/settings", body: settings, code: 200, ctype: jsn},
		{name: "settings unknown field", method: post, path: "/api/settings", body: `{"bogus":1}`, code: 400, want: "invalid settings"},
		{name: "settings garbage", method: post, path: "/api/settings", body: `nope`, code: 400, want: "invalid settings"},
		{name: "settings empty", method: post, path: "/api/settings", body: ``, code: 400, want: "invalid settings"},
		{name: "settings trailing", method: post, path: "/api/settings", body: settings + ` {}`, code: 400, want: "unexpected extra data"},
		{name: "settings oversize padding", method: post, path: "/api/settings", body: settings + spaces(5000), code: 400, want: "unexpected extra data"},
		{name: "settings oversize body", method: post, path: "/api/settings", body: `{"bogus":"` + strings.Repeat("a", 5000) + `"}`, code: 400, want: "invalid settings"},
		{name: "settings rejected values", method: post, path: "/api/settings", body: badSettings, code: 400, want: "poll interval must be 5–60 seconds"},

		// Power rules.
		{name: "power get", method: get, path: "/api/power", code: 200, ctype: jsn},
		{name: "power put", method: put, path: "/api/power", body: powerBody, code: 405, want: "method not allowed"},
		{name: "power no csrf", method: post, path: "/api/power", body: powerBody, noCSRF: true, code: 403, want: "invalid CSRF token"},
		{name: "power ok", method: post, path: "/api/power", body: powerBody, code: 200, ctype: jsn},
		{name: "power unknown field", method: post, path: "/api/power", body: `{"bogus":1}`, code: 400, want: "invalid power rules"},
		{name: "power garbage", method: post, path: "/api/power", body: `nope`, code: 400, want: "invalid power rules"},
		{name: "power trailing", method: post, path: "/api/power", body: powerBody + ` {}`, code: 400, want: "unexpected extra data"},
		{name: "power oversize padding", method: post, path: "/api/power", body: powerBody + spaces(40000), code: 400, want: "unexpected extra data"},
		{name: "power unsafe host", method: post, path: "/api/power", body: badPower, code: 400, want: "device x has invalid host, SSH user or port"},
		{name: "power bare get", variant: "bare", method: get, path: "/api/power", code: 503, want: "power automation unavailable"},
		{name: "power bare post", variant: "bare", method: post, path: "/api/power", body: powerBody, code: 503, want: "power automation unavailable"},
		{name: "power bare put", variant: "bare", method: put, path: "/api/power", code: 503, want: "power automation unavailable"},

		// Host shutdown settings.
		{name: "host-settings get", method: get, path: "/api/power/host-settings", code: 405, want: "method not allowed"},
		{name: "host-settings no csrf", method: post, path: "/api/power/host-settings", body: hostOK, noCSRF: true, code: 403, want: "invalid CSRF token"},
		{name: "host-settings unknown field", method: post, path: "/api/power/host-settings", body: `{"managed":true,"battery_percent":10,"runtime_minutes":3,"command":"shutdown"}`, code: 400, want: "invalid shutdown settings"},
		{name: "host-settings garbage", method: post, path: "/api/power/host-settings", body: `nope`, code: 400, want: "invalid shutdown settings"},
		{name: "host-settings empty", method: post, path: "/api/power/host-settings", body: ``, code: 400, want: "invalid shutdown settings"},
		{name: "host-settings trailing", method: post, path: "/api/power/host-settings", body: hostOK + ` {}`, code: 400, want: "invalid shutdown settings"},
		{name: "host-settings oversize", method: post, path: "/api/power/host-settings", body: hostOK + spaces(2000), code: 400, want: "invalid shutdown settings"},
		{name: "host-settings invalid", method: post, path: "/api/power/host-settings", body: `{"managed":true,"battery_percent":1,"runtime_minutes":3}`, code: 400, want: "battery shutdown threshold must be between 5 and 95 percent"},
		{name: "host-settings offline", variant: "offline", method: post, path: "/api/power/host-settings", body: hostOK, code: 409, want: "UPS is not confirmed ONLINE; shutdown settings cannot be changed"},
		{name: "host-settings no service", method: post, path: "/api/power/host-settings", body: hostOK, code: 503, want: "Local apcupsd settings service unavailable: check that the host settings socket is installed and enabled"},
		{name: "host-settings bare no service", variant: "bare", method: post, path: "/api/power/host-settings", body: hostOK, code: 503, want: "Local apcupsd settings service unavailable: check that the host settings socket is installed and enabled"},
		{name: "host-thresholds", method: get, path: "/api/power/host-thresholds", code: 200, ctype: jsn},
		{name: "host-thresholds post", method: post, path: "/api/power/host-thresholds", code: 405, want: "method not allowed"},

		// SSH test, key, state and status.
		{name: "test-ssh get", method: get, path: "/api/power/test-ssh", code: 405, want: "method not allowed"},
		{name: "test-ssh no csrf", method: post, path: "/api/power/test-ssh", body: `{"id":"x"}`, noCSRF: true, code: 403, want: "invalid CSRF token"},
		{name: "test-ssh unknown device", method: post, path: "/api/power/test-ssh", body: `{"id":"missing"}`, code: 404, want: "saved device not found"},
		{name: "test-ssh bare unknown device", variant: "bare", method: post, path: "/api/power/test-ssh", body: `{"id":"missing"}`, code: 404, want: "saved device not found"},
		{name: "test-ssh empty id", method: post, path: "/api/power/test-ssh", body: `{"id":""}`, code: 400, want: "invalid saved device ID"},
		{name: "test-ssh missing id", method: post, path: "/api/power/test-ssh", body: `{}`, code: 400, want: "invalid saved device ID"},
		{name: "test-ssh unknown field", method: post, path: "/api/power/test-ssh", body: `{"id":"x","extra":1}`, code: 400, want: "invalid saved device ID"},
		{name: "test-ssh trailing", method: post, path: "/api/power/test-ssh", body: `{"id":"x"} {}`, code: 400, want: "invalid saved device ID"},
		{name: "test-ssh garbage", method: post, path: "/api/power/test-ssh", body: `nope`, code: 400, want: "invalid saved device ID"},
		{name: "test-ssh oversize", method: post, path: "/api/power/test-ssh", body: `{"id":"x"}` + spaces(2000), code: 400, want: "invalid saved device ID"},
		{name: "ssh-key", method: get, path: "/api/power/ssh-key", code: 200, ctype: jsn, has: `"keys"`},
		{name: "ssh-key post", method: post, path: "/api/power/ssh-key", body: `{}`, code: 405, want: "method not allowed"},
		{name: "state", method: get, path: "/api/power/state", code: 200, ctype: jsn},
		{name: "state post", method: post, path: "/api/power/state", code: 405, want: "method not allowed"},
		{name: "state bare", variant: "bare", method: get, path: "/api/power/state", code: 503, want: "power automation unavailable"},
		{name: "state bare post", variant: "bare", method: post, path: "/api/power/state", code: 405, want: "method not allowed"},
		{name: "device status", method: get, path: "/api/power/status", code: 200, ctype: jsn, has: `"devices":[]`},
		{name: "device status post", method: post, path: "/api/power/status", body: `{}`, code: 405, want: "method not allowed"},
		{name: "device status bare", variant: "bare", method: get, path: "/api/power/status", code: 200, ctype: jsn, has: `"devices":[]`},

		// Email.
		{name: "email get", method: get, path: "/api/email", code: 200, ctype: jsn, has: `"settings"`},
		{name: "email put", method: put, path: "/api/email", body: mailBody, code: 405, want: "method not allowed"},
		{name: "email no csrf", method: post, path: "/api/email", body: mailBody, noCSRF: true, code: 403, want: "invalid CSRF token"},
		{name: "email ok", method: post, path: "/api/email", body: mailBody, code: 200, ctype: jsn},
		{name: "email unknown field", method: post, path: "/api/email", body: `{"bogus":1}`, code: 400, want: "invalid SMTP settings"},
		{name: "email garbage", method: post, path: "/api/email", body: `nope`, code: 400, want: "invalid SMTP settings"},
		{name: "email trailing", method: post, path: "/api/email", body: mailBody + ` {}`, code: 400, want: "unexpected extra data"},
		{name: "email oversize padding", method: post, path: "/api/email", body: mailBody + spaces(5000), code: 400, want: "unexpected extra data"},
		{name: "email rejected values", method: post, path: "/api/email", body: badMail, code: 400, want: "invalid SMTP hostname"},
		{name: "email bare get", variant: "bare", method: get, path: "/api/email", code: 503, want: "email reporting unavailable"},
		{name: "email bare put", variant: "bare", method: put, path: "/api/email", code: 503, want: "email reporting unavailable"},
		{name: "email test", method: post, path: "/api/email/test", code: 200, ctype: jsn, has: "Test email sent"},
		{name: "email test get", method: get, path: "/api/email/test", code: 405, want: "method not allowed"},
		{name: "email test no csrf", method: post, path: "/api/email/test", noCSRF: true, code: 403, want: "invalid CSRF token"},
		{name: "email test offline", variant: "offline", method: post, path: "/api/email/test", code: 409, want: "test email only available on verified mains power"},
		{name: "email test bare", variant: "bare", method: post, path: "/api/email/test", code: 503, want: "email reporting unavailable"},
		{name: "email test bare get", variant: "bare", method: get, path: "/api/email/test", code: 503, want: "email reporting unavailable"},

		// History.
		{name: "history", method: get, path: "/api/history", code: 200, ctype: jsn},
		{name: "history 7 days", method: get, path: "/api/history?days=7", code: 200, ctype: jsn},
		{name: "history post", method: post, path: "/api/history", code: 405, want: "method not allowed"},
		{name: "history non-numeric", method: get, path: "/api/history?days=abc", code: 400, want: "invalid range"},
		{name: "history bad range", method: get, path: "/api/history?days=31", code: 400, want: "invalid range"},
		{name: "history bare", variant: "bare", method: get, path: "/api/history", code: 503, want: "history unavailable"},
		{name: "history bare post", variant: "bare", method: post, path: "/api/history", code: 405, want: "method not allowed"},
		{name: "battery history", method: get, path: "/api/history/battery", code: 200, ctype: jsn},
		{name: "battery history post", method: post, path: "/api/history/battery", code: 405, want: "method not allowed"},
		{name: "battery history non-numeric", method: get, path: "/api/history/battery?days=abc", code: 400, want: "invalid range"},
		{name: "battery history bad range", method: get, path: "/api/history/battery?days=31", code: 400, want: "invalid range"},
		{name: "battery history bare", variant: "bare", method: get, path: "/api/history/battery", code: 503, want: "history unavailable"},
		{name: "battery history bare bad days", variant: "bare", method: get, path: "/api/history/battery?days=abc", code: 503, want: "history unavailable"},

		// Security log.
		{name: "fail2ban", method: get, path: "/api/security/fail2ban", code: 200, ctype: jsn, has: "Fail2ban event log is not available. Check the installation and service."},
		{name: "fail2ban post", method: post, path: "/api/security/fail2ban", code: 405, want: "method not allowed"},

		// Password change and logout (these end the session, so each gets its own).
		{name: "change-password get", method: get, path: "/change-password", code: 405, want: "method not allowed"},
		{name: "change-password no csrf", method: post, path: "/change-password", body: "old=a&new=b", noCSRF: true, code: 403, want: "invalid CSRF token"},
		{name: "change-password oversize", method: post, path: "/change-password", body: "old=" + strings.Repeat("a", 5000), code: 400, want: "bad request"},
		{name: "change-password wrong old", method: post, path: "/change-password", body: "old=wrong&new=another long password", code: 400, ctype: html, has: "Current password incorrect or new password invalid (minimum 12 characters)."},
		{name: "logout get", method: get, path: "/logout", code: 405, want: "method not allowed"},
		{name: "logout no csrf", method: post, path: "/logout", noCSRF: true, code: 403, want: "invalid CSRF token"},
		{name: "logout", method: post, path: "/logout", code: 303, loc: "/login", fresh: true},
		{name: "change-password ok", method: post, path: "/change-password", body: "old=" + url.QueryEscape(routesPassphrase) + "&new=another+long+password", code: 303, loc: "/login?changed=1", fresh: true},
	}

	envs := map[string]*routeEnv{}
	for _, v := range []string{"", "bare", "offline"} {
		envs[v] = buildRouteEnv(t, v)
	}
	before := fail2banLogPath
	fail2banLogPath = filepath.Join(t.TempDir(), "missing.log")
	defer func() { fail2banLogPath = before }()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := envs[tc.variant]
			cookie, csrf := env.cookie, env.csrf
			if tc.fresh {
				token, c, err := env.auth.Login("127.0.0.1", "admin", routesPassphrase)
				if err != nil {
					t.Fatal(err)
				}
				cookie, csrf = &http.Cookie{Name: "ups_bridge_session", Value: token}, c
			}
			if tc.anon {
				cookie, csrf = nil, ""
			}
			if tc.noCSRF {
				csrf = ""
			}
			w := request(env.h, tc.method, tc.path, tc.body, cookie, csrf)
			if w.Code != tc.code {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.code, w.Body.String())
			}
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control %q", got)
			}
			if tc.want != "" {
				if w.Body.String() != tc.want+"\n" {
					t.Errorf("body %q, want %q", w.Body.String(), tc.want+"\n")
				}
				if got := w.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
					t.Errorf("Content-Type %q", got)
				}
			}
			if tc.has != "" && !strings.Contains(w.Body.String(), tc.has) {
				t.Errorf("body %q lacks %q", w.Body.String(), tc.has)
			}
			if tc.ctype != "" {
				if got := w.Header().Get("Content-Type"); got != tc.ctype {
					t.Errorf("Content-Type %q, want %q", got, tc.ctype)
				}
			}
			if tc.loc != "" {
				if got := w.Header().Get("Location"); got != tc.loc {
					t.Errorf("Location %q, want %q", got, tc.loc)
				}
			}
		})
	}
}

func TestCSVDownloadHeaders(t *testing.T) {
	env := buildRouteEnv(t, "")
	w := request(env.h, "GET", "/api/events.csv", "", env.cookie, "")
	if got := w.Header().Get("Content-Disposition"); got != `attachment; filename="apc-ups-bridge-events.csv"` {
		t.Fatalf("Content-Disposition %q", got)
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	env := buildRouteEnv(t, "")
	form := url.Values{"username": {"admin"}, "password": {routesPassphrase}}.Encode()
	w := request(env.h, "POST", "/login", form, nil, "")
	cookies := w.Result().Cookies()
	if w.Code != 303 || len(cookies) != 1 {
		t.Fatalf("login %d, %d cookies", w.Code, len(cookies))
	}
	c := cookies[0]
	if c.Name != "ups_bridge_session" || c.Value == "" || c.Path != "/" || !c.HttpOnly || c.Secure || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 8*3600 {
		t.Fatalf("login cookie %+v", c)
	}
	csrf, ok := env.auth.Session(c.Value)
	if !ok {
		t.Fatal("session not valid")
	}
	w = request(env.h, "POST", "/logout", "", c, csrf)
	cookies = w.Result().Cookies()
	if w.Code != 303 || len(cookies) != 1 {
		t.Fatalf("logout %d, %d cookies", w.Code, len(cookies))
	}
	c = cookies[0]
	if c.Name != "ups_bridge_session" || c.Value != "" || c.Path != "/" || !c.HttpOnly || c.Secure || c.SameSite != http.SameSiteStrictMode || c.MaxAge != -1 {
		t.Fatalf("logout cookie %+v", c)
	}
}
