package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/auth"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/config"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/history"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/notify"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

const testPassphrase = "long secret passphrase"

type testReader struct{}

func (testReader) Read(context.Context) (apc.Snapshot, error) {
	v := 91.0
	return apc.Snapshot{Power: apc.Online, Status: "ONLINE", BatteryPercent: &v}, nil
}

// fixture is a handler wired to real stores in a temporary directory, with one
// signed-in session. Optional parts are switched on with fixtureOption values.
type fixture struct {
	Handler    *Handler
	Auth       *auth.Store
	Dir        string
	Cookie     *http.Cookie // signed-in session
	CSRF       string       // token that goes with Cookie
	Power      *power.Controller
	PowerStore *power.Store
	MailStore  *notify.Store
}

type fixtureOptions struct {
	power   bool
	history bool
	mail    *notify.Config
	noPoll  bool
}

type fixtureOption func(*fixtureOptions)

// withPower adds a power controller whose executor does nothing.
func withPower() fixtureOption { return func(o *fixtureOptions) { o.power = true } }

// withHistory adds a battery history store.
func withHistory() fixtureOption { return func(o *fixtureOptions) { o.history = true } }

// withMail adds an email engine whose saved settings are cfg.
func withMail(cfg notify.Config) fixtureOption { return func(o *fixtureOptions) { o.mail = &cfg } }

// withoutPoll leaves the monitor without a reading, so the UPS is not
// confirmed online.
func withoutPoll() fixtureOption { return func(o *fixtureOptions) { o.noPoll = true } }

func newFixture(t *testing.T, opts ...fixtureOption) *fixture {
	t.Helper()
	var o fixtureOptions
	for _, opt := range opts {
		opt(&o)
	}
	dir := t.TempDir()
	logs := filepath.Join(dir, "events")
	f := &fixture{Dir: dir}
	authPath := filepath.Join(dir, "auth.json")
	if err := auth.Initialize(authPath, testPassphrase); err != nil {
		t.Fatal(err)
	}
	var err error
	if f.Auth, err = auth.Open(authPath); err != nil {
		t.Fatal(err)
	}
	prefs, err := config.Open(filepath.Join(dir, "settings.json"), config.Settings{PollIntervalSeconds: 10, LowBatteryPercent: 20, RetentionDays: 365}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mon, err := monitor.New(testReader{}, events.FileWriter{Dir: logs}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !o.noPoll {
		if err = mon.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	var ctrl *power.Controller
	if o.power {
		if f.PowerStore, err = power.OpenConfig(filepath.Join(dir, "power.json")); err != nil {
			t.Fatal(err)
		}
		ctrl, err = power.OpenController(f.PowerStore, filepath.Join(dir, "power-state.json"), events.FileWriter{Dir: logs}, inertExec{})
		if err != nil {
			t.Fatal(err)
		}
		f.Power = ctrl
	}
	if f.Handler, err = New(mon, f.Auth, prefs, logs, ctrl); err != nil {
		t.Fatal(err)
	}
	if o.history {
		f.Handler.History = history.New(filepath.Join(dir, "history"))
	}
	if o.mail != nil {
		if f.MailStore, err = notify.OpenConfig(filepath.Join(dir, "email.json")); err != nil {
			t.Fatal(err)
		}
		if err = f.MailStore.Save(*o.mail); err != nil {
			t.Fatal(err)
		}
		engine, err := notify.Open(f.MailStore, filepath.Join(dir, "mail-state.json"), logs, events.FileWriter{Dir: logs}, mailSender{})
		if err != nil {
			t.Fatal(err)
		}
		f.Handler.Mail = engine
	}
	token, csrf, err := f.Auth.Login("127.0.0.1", "admin", testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	f.Cookie = &http.Cookie{Name: "ups_bridge_session", Value: token}
	f.CSRF = csrf
	return f
}

func request(h http.Handler, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if strings.HasPrefix(body, "{") {
		r.Header.Set("Content-Type", "application/json")
	} else if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestRoutesAndAuthentication(t *testing.T) {
	f := newFixture(t)
	h, a := f.Handler, f.Auth
	if w := request(h, "GET", "/api/status", "", nil, ""); w.Code != 401 {
		t.Fatalf("API unprotected: %d", w.Code)
	}
	if w := request(h, "GET", "/", "", nil, ""); w.Code != 303 {
		t.Fatalf("dashboard not protected: %d", w.Code)
	}
	if w := request(h, "GET", "/assets/app.css", "", nil, ""); w.Code != 200 {
		t.Fatalf("login CSS missing: %d", w.Code)
	}
	if w := request(h, "GET", "/assets/app.js", "", nil, ""); w.Code != 303 {
		t.Fatalf("app JS exposed without login: %d", w.Code)
	}
	loginForm := url.Values{"username": {"admin"}, "password": {testPassphrase}}.Encode()
	logged := request(h, "POST", "/login", loginForm, nil, "")
	if logged.Code != 303 {
		t.Fatalf("login status %d: %s", logged.Code, logged.Body.String())
	}
	cookies := logged.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	ck := cookies[0]
	if !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure cookie flags")
	}
	csrf, ok := a.Session(ck.Value)
	if !ok {
		t.Fatal("session not valid")
	}
	if w := request(h, "GET", "/api/status", "", ck, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "91") {
		t.Fatalf("UPS data missing: %d %s", w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/api/report", "", ck, ""); w.Code != 200 {
		t.Fatalf("report missing: %d", w.Code)
	}
	value := config.Settings{PollIntervalSeconds: 30, LowBatteryPercent: 15.5, RetentionDays: 0}
	b, _ := json.Marshal(value)
	if w := request(h, "POST", "/api/settings", string(b), ck, ""); w.Code != 403 {
		t.Fatalf("CSRF bypass: %d", w.Code)
	}
	if w := request(h, "POST", "/api/settings", string(b), ck, csrf); w.Code != 200 {
		t.Fatalf("settings update failed: %d %s", w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/api/settings", "", ck, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "15.5") {
		t.Fatalf("new settings not displayed: %d %s", w.Code, w.Body.String())
	}
	if w := request(h, "POST", "/logout", url.Values{"csrf": {csrf}}.Encode(), ck, ""); w.Code != 303 {
		t.Fatalf("logout failed: %d", w.Code)
	}
	if w := request(h, "GET", "/api/status", "", ck, ""); w.Code != 401 {
		t.Fatal("logout didn't revoke session")
	}
}
func TestPasswordChangeNeedsCSRF(t *testing.T) {
	f := newFixture(t)
	body := url.Values{"old": {testPassphrase}, "new": {"new secret longer password"}}.Encode()
	if w := request(f.Handler, "POST", "/change-password", body, f.Cookie, ""); w.Code != 403 {
		t.Fatalf("password change CSRF missing: %d", w.Code)
	}
	if w := request(f.Handler, "POST", "/change-password", body+"&csrf="+url.QueryEscape(f.CSRF), f.Cookie, ""); w.Code != 303 {
		t.Fatalf("password change: %d %s", w.Code, w.Body.String())
	}
	if _, ok := f.Auth.Session(f.Cookie.Value); ok {
		t.Fatal("sessions not revoked")
	}
}

func TestFirmwareRouteAuthentication(t *testing.T) {
	f := newFixture(t)
	if w := request(f.Handler, "GET", "/api/firmware", "", nil, ""); w.Code != 401 {
		t.Fatalf("firmware API exposed: %d", w.Code)
	}
	if w := request(f.Handler, "GET", "/api/firmware", "", f.Cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "\"status\":\"unknown\"") {
		t.Fatalf("firmware API failed: %d %s", w.Code, w.Body.String())
	}
	if w := request(f.Handler, "POST", "/api/firmware", "", f.Cookie, ""); w.Code != 405 {
		t.Fatalf("firmware API permits mutation: %d", w.Code)
	}
}

func TestFirmwareMovedToSettings(t *testing.T) {
	f := newFixture(t)
	w := request(f.Handler, "GET", "/settings", "", f.Cookie, "")
	if w.Code != 200 {
		t.Fatalf("settings returned %d", w.Code)
	}
	for _, value := range []string{"id=\"firmware-installed\"", "id=\"firmware-status\"", "id=\"firmware-ups-id\"", "id=\"firmware-warning\""} {
		if !strings.Contains(w.Body.String(), value) {
			t.Errorf("missing firmware display: %s", value)
		}
	}
}

func TestPiShutdownSettingsAuthorizationAndValidation(t *testing.T) {
	f := newFixture(t)
	h, cookie, csrf := f.Handler, f.Cookie, f.CSRF
	body := `{"managed":true,"battery_percent":10,"runtime_minutes":3}`
	if w := request(h, "POST", "/api/power/host-settings", body, nil, ""); w.Code != 401 {
		t.Fatalf("unauthenticated request accepted: %d", w.Code)
	}
	if w := request(h, "POST", "/api/power/host-settings", body, cookie, ""); w.Code != 403 {
		t.Fatalf("CSRF bypass: %d", w.Code)
	}
	if w := request(h, "GET", "/api/power/host-settings", "", cookie, csrf); w.Code != 405 {
		t.Fatalf("GET mutation allowed: %d", w.Code)
	}
	if w := request(h, "POST", "/api/power/host-settings", `{"managed":true,"battery_percent":1,"runtime_minutes":3}`, cookie, csrf); w.Code != 400 {
		t.Fatalf("invalid thresholds accepted: %d", w.Code)
	}
	if w := request(h, "POST", "/api/power/host-settings", `{"managed":true,"battery_percent":10,"runtime_minutes":3,"timeout_minutes":121}`, cookie, csrf); w.Code != 400 {
		t.Fatalf("invalid timer accepted: %d", w.Code)
	}
	if w := request(h, "POST", "/api/power/host-settings", `{"managed":true,"battery_percent":10,"runtime_minutes":3,"command":"shutdown"}`, cookie, csrf); w.Code != 400 {
		t.Fatalf("extra field accepted: %d", w.Code)
	}
	if w := request(h, "POST", "/api/power/host-settings", body, cookie, csrf); w.Code != 503 {
		t.Fatalf("missing socket should block operation: %d", w.Code)
	}
}
