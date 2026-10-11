package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/auth"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/config"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

type inertExec struct{}

func (inertExec) Shutdown(context.Context, power.Device) error { return nil }
func (inertExec) Wake(context.Context, power.Device) error     { return nil }
func (inertExec) Reachable(context.Context, power.Device) bool { return false }
func TestPowerRoutesAuthorizedAndValidated(t *testing.T) {
	dir := t.TempDir()
	authpath := filepath.Join(dir, "auth.json")
	if err := auth.Initialize(authpath, "long secret passphrase"); err != nil {
		t.Fatal(err)
	}
	admin, err := auth.Open(authpath)
	if err != nil {
		t.Fatal(err)
	}
	mon, err := monitor.New(testReader{}, events.FileWriter{Dir: filepath.Join(dir, "logs")}, 20)
	if err != nil {
		t.Fatal(err)
	}
	prefs, err := config.Open(filepath.Join(dir, "prefs.json"), config.Settings{PollIntervalSeconds: 10, LowBatteryPercent: 20, RetentionDays: 365}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pstore, err := power.OpenConfig(filepath.Join(dir, "power.json"))
	if err != nil {
		t.Fatal(err)
	}
	controller, err := power.OpenController(pstore, filepath.Join(dir, "power-state.json"), events.FileWriter{Dir: filepath.Join(dir, "logs")}, inertExec{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New(mon, admin, prefs, filepath.Join(dir, "logs"), controller)
	if err != nil {
		t.Fatal(err)
	}
	if r := request(h, "GET", "/api/power", "", nil, ""); r.Code != 401 {
		t.Fatalf("unprotected config %d", r.Code)
	}
	token, csrf, err := admin.Login("127.0.0.1", "admin", "long secret passphrase")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "ups_bridge_session", Value: token}
	if r := request(h, "GET", "/power", "", cookie, ""); r.Code != 200 || !strings.Contains(r.Body.String(), "Devices &amp; power rules") {
		t.Fatalf("missing power form %d", r.Code)
	}
	if r := request(h, "GET", "/api/power/state", "", cookie, ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"enabled":false`) {
		t.Fatalf("wrong default state %d %s", r.Code, r.Body.String())
	}
	v := power.Defaults()
	v.Devices = []power.Device{{ID: "host-1", Name: "Host", Host: "10.0.0.11", SSHUser: "root", SSHPort: 22, MAC: "00:11:22:33:44:55", WakePort: 9, ShutdownEnabled: true, WakeEnabled: true}}
	v.Enabled = true
	b, _ := json.Marshal(v)
	if r := request(h, "POST", "/api/power", string(b), cookie, ""); r.Code != 403 {
		t.Fatalf("missing CSRF allowed: %d", r.Code)
	}
	if r := request(h, "POST", "/api/power", string(b), cookie, csrf); r.Code != 200 {
		t.Fatalf("failed save: %d %s", r.Code, r.Body.String())
	}
	if !pstore.Get().Enabled {
		t.Fatal("configuration not stored")
	}
	v.Devices[0].Host = "-oProxyCommand=evil"
	b, _ = json.Marshal(v)
	if r := request(h, "POST", "/api/power", string(b), cookie, csrf); r.Code != 400 {
		t.Fatalf("unsafe hostname accepted: %d", r.Code)
	}
}

var _ apc.Reader = testReader{}

func TestSSHPreflightRequiresAuthenticationAndCSRF(t *testing.T) {
 dir:=t.TempDir()
 authpath:=filepath.Join(dir,"auth.json")
 if err:=auth.Initialize(authpath,"long secret passphrase");err!=nil{t.Fatal(err)}
 admin,_:=auth.Open(authpath)
 mon,_:=monitor.New(testReader{},events.FileWriter{Dir:filepath.Join(dir,"logs")},20)
 prefs,_:=config.Open(filepath.Join(dir,"prefs.json"),config.Settings{PollIntervalSeconds:10,LowBatteryPercent:20,RetentionDays:365},nil)
 store,_:=power.OpenConfig(filepath.Join(dir,"power.json"))
 ctrl,_:=power.OpenController(store,filepath.Join(dir,"power-state.json"),events.FileWriter{Dir:filepath.Join(dir,"logs")},inertExec{})
 h,_:=New(mon,admin,prefs,filepath.Join(dir,"logs"),ctrl)
 payload:=`{"id":"missing"}`
 if w:=request(h,"POST","/api/power/test-ssh",payload,nil,"");w.Code!=401{t.Fatalf("unauthenticated: %d",w.Code)}
 token,csrf,_:=admin.Login("127.0.0.1","admin","long secret passphrase")
 cookie:=&http.Cookie{Name:"ups_bridge_session",Value:token}
 if w:=request(h,"POST","/api/power/test-ssh",payload,cookie,"");w.Code!=403{t.Fatalf("CSRF bypass: %d",w.Code)}
 if w:=request(h,"GET","/api/power/test-ssh","",cookie,csrf);w.Code!=405{t.Fatalf("GET test allowed: %d",w.Code)}
 if w:=request(h,"POST","/api/power/test-ssh",payload,cookie,csrf);w.Code!=404{t.Fatalf("unknown device allowed: %d",w.Code)}
 if w:=request(h,"GET","/api/power/host-thresholds","",cookie,"");w.Code!=200{t.Fatalf("native threshold view: %d",w.Code)}
}
