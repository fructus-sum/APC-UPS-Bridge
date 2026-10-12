package web

import (
	"strings"
	"testing"
)

func TestThirdHistoryChartIsActuallyWired(t *testing.T) {
	f := newFixture(t)
	page := request(f.Handler, "GET", "/history", "", f.Cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), `id="battery-chart"`) {
		t.Fatalf("missing third chart in page: %d", page.Code)
	}
	js := request(f.Handler, "GET", "/assets/history.js", "", f.Cookie, "")
	if js.Code != 200 || !strings.Contains(js.Body.String(), "drawBattery(report)") {
		t.Fatalf("third chart not invoked: %d", js.Code)
	}
}
