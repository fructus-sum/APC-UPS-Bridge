package web

import (
	"encoding/json"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/history"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryPageAndAPIProtected(t *testing.T) {
	h, auth := setupWeb(t)
	AttachHistory(h, history.New(filepath.Join(t.TempDir(), "history")))
	for _, path := range []string{"/history", "/api/history", "/assets/history.js"} {
		w := request(h, "GET", path, "", nil, "")
		if w.Code != http.StatusSeeOther && w.Code != http.StatusUnauthorized {
			t.Fatalf("not protected: %s status %d", path, w.Code)
		}
	}
	token, _, err := auth.Login("127.0.0.1", "admin", "long secret passphrase")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: "ups_bridge_session", Value: token}
	w := request(h, "GET", "/history", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "history-days") {
		t.Fatalf("page unavailable %d %s", w.Code, w.Body.String())
	}
	w = request(h, "GET", "/api/history", "", cookie, "")
	if w.Code != 200 {
		t.Fatalf("API status %d %s", w.Code, w.Body.String())
	}
	var data history.Result
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Points) != 14*24*4+1 || data.IntervalSeconds != int((15*time.Minute).Seconds()) {
		t.Fatalf("bad default axis: %+v", data)
	}
	if w = request(h, "GET", "/api/history?days=31", "", cookie, ""); w.Code != 400 {
		t.Fatalf("bad range allowed %d", w.Code)
	}
	if w = request(h, "GET", "/assets/history.js", "", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "createElementNS") {
		t.Fatalf("missing JS %d", w.Code)
	}
}
