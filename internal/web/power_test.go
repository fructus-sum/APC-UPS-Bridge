package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

type inertExec struct{}

func (inertExec) Shutdown(context.Context, power.Device) error { return nil }
func (inertExec) Wake(context.Context, power.Device) error     { return nil }
func (inertExec) Reachable(context.Context, power.Device) bool { return false }
func TestPowerRoutesAuthorizedAndValidated(t *testing.T) {
	f := newFixture(t, withPower())
	h, cookie, csrf := f.Handler, f.Cookie, f.CSRF
	if r := request(h, "GET", "/api/power", "", nil, ""); r.Code != 401 {
		t.Fatalf("unprotected config %d", r.Code)
	}
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
	if !f.PowerStore.Get().Enabled {
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
	f := newFixture(t, withPower())
	h, cookie, csrf := f.Handler, f.Cookie, f.CSRF
	payload := `{"id":"missing"}`
	if w := request(h, "POST", "/api/power/test-ssh", payload, nil, ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	if w := request(h, "POST", "/api/power/test-ssh", payload, cookie, ""); w.Code != 403 {
		t.Fatalf("CSRF bypass: %d", w.Code)
	}
	if w := request(h, "GET", "/api/power/test-ssh", "", cookie, csrf); w.Code != 405 {
		t.Fatalf("GET test allowed: %d", w.Code)
	}
	if w := request(h, "POST", "/api/power/test-ssh", payload, cookie, csrf); w.Code != 404 {
		t.Fatalf("unknown device allowed: %d", w.Code)
	}
	if w := request(h, "GET", "/api/power/host-thresholds", "", cookie, ""); w.Code != 200 {
		t.Fatalf("native threshold view: %d", w.Code)
	}
}

func TestSSHKeyEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t, withPower())
	h, cookie, csrf := f.Handler, f.Cookie, f.CSRF
	if w := request(h, "GET", "/api/power/ssh-key", "", nil, ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	if w := request(h, "POST", "/api/power/ssh-key", "{}", cookie, csrf); w.Code != 405 {
		t.Fatalf("POST allowed: %d", w.Code)
	}
	w := request(h, "GET", "/api/power/ssh-key", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"keys":[]`) {
		t.Fatalf("key list: %d %s", w.Code, w.Body.String())
	}
}
