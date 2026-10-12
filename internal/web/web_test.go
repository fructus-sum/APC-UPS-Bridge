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
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
)

type testReader struct{}

func (testReader) Read(context.Context) (apc.Snapshot, error) {
	v := 91.0
	return apc.Snapshot{Power: apc.Online, Status: "ONLINE", BatteryPercent: &v}, nil
}
func setupWeb(t *testing.T) (http.Handler, *auth.Store) {
	t.Helper()
	dir := t.TempDir()
	pass := filepath.Join(dir, "auth.json")
	if err := auth.Initialize(pass, "long secret passphrase"); err != nil {
		t.Fatal(err)
	}
	a, err := auth.Open(pass)
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Open(filepath.Join(dir, "settings.json"), config.Settings{PollIntervalSeconds: 10, LowBatteryPercent: 20, RetentionDays: 365}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := monitor.New(testReader{}, events.FileWriter{Dir: filepath.Join(dir, "events")}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	h, err := New(m, a, c, filepath.Join(dir, "events"))
	if err != nil {
		t.Fatal(err)
	}
	return h, a
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
	h, a := setupWeb(t)
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
	loginForm := url.Values{"username": {"admin"}, "password": {"long secret passphrase"}}.Encode()
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
	h, a := setupWeb(t)
	token, csrf, err := a.Login("127.0.0.1", "admin", "long secret passphrase")
	if err != nil {
		t.Fatal(err)
	}
	ck := &http.Cookie{Name: "ups_bridge_session", Value: token}
	body := url.Values{"old": {"long secret passphrase"}, "new": {"new secret longer password"}}.Encode()
	if w := request(h, "POST", "/change-password", body, ck, ""); w.Code != 403 {
		t.Fatalf("password change CSRF missing: %d", w.Code)
	}
	if w := request(h, "POST", "/change-password", body+"&csrf="+url.QueryEscape(csrf), ck, ""); w.Code != 303 {
		t.Fatalf("password change: %d %s", w.Code, w.Body.String())
	}
	if _, ok := a.Session(token); ok {
		t.Fatal("sessions not revoked")
	}
}

func TestFirmwareRouteAuthentication(t *testing.T) {
 h,a:=setupWeb(t)
 if w:=request(h,"GET","/api/firmware","",nil,"");w.Code!=401{t.Fatalf("firmware API exposed: %d",w.Code)}
 token,_,err:=a.Login("127.0.0.1","admin","long secret passphrase");if err!=nil{t.Fatal(err)}
 cookie:=&http.Cookie{Name:"ups_bridge_session",Value:token}
 if w:=request(h,"GET","/api/firmware","",cookie,"");w.Code!=200||!strings.Contains(w.Body.String(),"\"status\":\"unknown\""){t.Fatalf("firmware API failed: %d %s",w.Code,w.Body.String())}
 if w:=request(h,"POST","/api/firmware","",cookie,"");w.Code!=405{t.Fatalf("firmware API permits mutation: %d",w.Code)}
}

func TestFirmwareMovedToSettings(t *testing.T) {
 h,a:=setupWeb(t)
 token,_,err:=a.Login("127.0.0.1","admin","long secret passphrase");if err!=nil{t.Fatal(err)}
 cookie:=&http.Cookie{Name:"ups_bridge_session",Value:token}
 w:=request(h,"GET","/settings","",cookie,"")
 if w.Code!=200{t.Fatalf("settings returned %d",w.Code)}
 for _,value:=range []string{"id=\"firmware-installed\"","id=\"firmware-status\"","id=\"firmware-ups-id\"","id=\"firmware-warning\""}{
  if !strings.Contains(w.Body.String(),value){t.Errorf("missing firmware display: %s",value)}
 }
}

func TestPiShutdownSettingsAuthorizationAndValidation(t *testing.T) {
 h,a:=setupWeb(t)
 body:=`{"managed":true,"battery_percent":10,"runtime_minutes":3}`
 if w:=request(h,"POST","/api/power/host-settings",body,nil,"");w.Code!=401{t.Fatalf("unauthenticated request accepted: %d",w.Code)}
 token,csrf,err:=a.Login("127.0.0.1","admin","long secret passphrase");if err!=nil{t.Fatal(err)}
 cookie:=&http.Cookie{Name:"ups_bridge_session",Value:token}
 if w:=request(h,"POST","/api/power/host-settings",body,cookie,"");w.Code!=403{t.Fatalf("CSRF bypass: %d",w.Code)}
 if w:=request(h,"GET","/api/power/host-settings","",cookie,csrf);w.Code!=405{t.Fatalf("GET mutation allowed: %d",w.Code)}
 if w:=request(h,"POST","/api/power/host-settings",`{"managed":true,"battery_percent":1,"runtime_minutes":3}`,cookie,csrf);w.Code!=400{t.Fatalf("invalid thresholds accepted: %d",w.Code)}
 if w:=request(h,"POST","/api/power/host-settings",`{"managed":true,"battery_percent":10,"runtime_minutes":3,"timeout_minutes":121}`,cookie,csrf);w.Code!=400{t.Fatalf("invalid timer accepted: %d",w.Code)}
 if w:=request(h,"POST","/api/power/host-settings",`{"managed":true,"battery_percent":10,"runtime_minutes":3,"command":"shutdown"}`,cookie,csrf);w.Code!=400{t.Fatalf("extra field accepted: %d",w.Code)}
 if w:=request(h,"POST","/api/power/host-settings",body,cookie,csrf);w.Code!=503{t.Fatalf("missing socket should block operation: %d",w.Code)}
}
