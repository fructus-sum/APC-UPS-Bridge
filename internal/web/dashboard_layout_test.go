package web

import (
	"strings"
	"testing"
)

func TestDashboardCollapsedSystemDetailsAndFirmwareLocation(t *testing.T) {
	f := newFixture(t)
	dash := request(f.Handler, "GET", "/", "", f.Cookie, "")
	body := dash.Body.String()
	for _, part := range []string{`id="model"`, `<details id="system-information"`, `System information`, `id="device-status-list"`, `id="firmware-notice" class="message firmware-warning" role="status" hidden`, `href="/settings"`} {
		if !strings.Contains(body, part) {
			t.Errorf("missing dashboard section: %s", part)
		}
	}
	if strings.Contains(body, `dashboard-remotes`) {
		t.Error("duplicate remote device list on dashboard")
	}
	for _, part := range []string{`id="health-battery-voltage"`, `id="health-test-result"`, `id="battery-test-guide"`, `id="battery-test-instructions" hidden`} {
		if !strings.Contains(body, part) {
			t.Errorf("missing battery health: %s", part)
		}
	}
	if strings.Contains(body, `id="firmware-installed"`) {
		t.Fatal("firmware still on dashboard")
	}
	settings := request(f.Handler, "GET", "/settings", "", f.Cookie, "")
	if !strings.Contains(settings.Body.String(), `id="firmware-installed"`) {
		t.Fatal("firmware not moved to settings")
	}
	powerPage := request(f.Handler, "GET", "/power", "", f.Cookie, "")
	if !strings.Contains(powerPage.Body.String(), `id="device-list"`) {
		t.Fatal("missing remote device editor")
	}
	js := request(f.Handler, "GET", "/assets/app.js", "", f.Cookie, "")
	for _, part := range []string{"device-details", "details.open=!data.id", "refreshDeviceStatus();setInterval(refreshDeviceStatus", "refreshFirmware();setInterval(refreshFirmware,60000)"} {
		if !strings.Contains(js.Body.String(), part) {
			t.Errorf("missing browser wiring: %s", part)
		}
	}
	if strings.Contains(js.Body.String(), "devices/status") {
		t.Error("browser still uses the removed per-request device check")
	}
}
