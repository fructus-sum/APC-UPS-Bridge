package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestDashboardCollapsedSystemDetailsAndFirmwareLocation(t *testing.T) {
	h, a := setupWeb(t)
	token, _, err := a.Login("127.0.0.1", "admin", "long secret passphrase")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "ups_bridge_session", Value: token}
	dash := request(h, "GET", "/", "", cookie, "")
	body := dash.Body.String()
	for _, part := range []string{`id="ups-model-heading"`, `id="model"`, `<details id="system-information"`, `System information`, `id="device-status-list"`, `id="firmware-notice" class="message firmware-warning" role="status" hidden`, `href="/settings"`} {
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
	settings := request(h, "GET", "/settings", "", cookie, "")
	if !strings.Contains(settings.Body.String(), `id="firmware-installed"`) {
		t.Fatal("firmware not moved to settings")
	}
	powerPage := request(h, "GET", "/power", "", cookie, "")
	if !strings.Contains(powerPage.Body.String(), `id="device-list"`) {
		t.Fatal("missing remote device editor")
	}
	js := request(h, "GET", "/assets/app.js", "", cookie, "")
	for _, part := range []string{"device-details", "details.open=!data.id", "refreshDeviceStatus();setInterval(refreshDeviceStatus", "refreshFirmware();setInterval(refreshFirmware,60000)"} {
		if !strings.Contains(js.Body.String(), part) {
			t.Errorf("missing browser wiring: %s", part)
		}
	}
	if strings.Contains(js.Body.String(), "devices/status") {
		t.Error("browser still uses the removed per-request device check")
	}
}
