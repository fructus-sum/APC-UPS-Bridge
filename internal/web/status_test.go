package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

type inertProber struct{}

func (inertProber) Reachable(context.Context, power.Device) bool { return false }
func (inertProber) Ping(context.Context, string) (time.Duration, error) {
	return 0, power.ErrPingTimeout
}

func TestPowerStatusRoute(t *testing.T) {
	f := newFixture(t, withPower())
	h, cookie, csrf, ctrl, store := f.Handler, f.Cookie, f.CSRF, f.Power, f.PowerStore
	if w := request(h, "GET", "/api/power/status", "", nil, ""); w.Code != 401 {
		t.Fatalf("unauthenticated: %d", w.Code)
	}
	if w := request(h, "POST", "/api/power/status", "{}", cookie, csrf); w.Code != 405 {
		t.Fatalf("POST allowed: %d", w.Code)
	}
	// Without a monitor the list is empty, not an error.
	w := request(h, "GET", "/api/power/status", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"devices":[]`) || !strings.Contains(w.Body.String(), `"interval_seconds":60`) {
		t.Fatalf("no monitor: %d %s", w.Code, w.Body.String())
	}

	monitorOnline := power.NewStatusMonitor(ctrl.Config, inertProber{})
	AttachStatus(h, monitorOnline)
	v := power.Defaults()
	v.Devices = []power.Device{{ID: "host-1", Name: "Host", Host: "192.0.2.11", SSHUser: "root", SSHPort: 22, OnlineCheck: "ssh_port"}}
	b, _ := json.Marshal(v)
	if r := request(h, "POST", "/api/power", string(b), cookie, csrf); r.Code != 200 {
		t.Fatalf("online_check rejected: %d %s", r.Code, r.Body.String())
	}
	if store.Get().Devices[0].OnlineCheck != "ssh_port" {
		t.Fatal("online_check not stored")
	}
	w = request(h, "GET", "/api/power/status", "", cookie, "")
	var got struct {
		Devices []power.DeviceStatus `json:"devices"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Devices) != 1 || got.Devices[0].ID != "host-1" || got.Devices[0].State != "unknown" || got.Devices[0].Method != "ssh_port" {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	v.Devices[0].OnlineCheck = "tcp"
	b, _ = json.Marshal(v)
	if r := request(h, "POST", "/api/power", string(b), cookie, csrf); r.Code != 400 {
		t.Fatalf("invalid online_check accepted: %d", r.Code)
	}
}
