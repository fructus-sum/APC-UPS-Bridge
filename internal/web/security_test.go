package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityLogAuthenticatedReadOnly(t *testing.T) {
	h, a := setupWeb(t)
	file := filepath.Join(t.TempDir(), "security.log")
	data := "2026-10-12T09:00:00Z ban 192.0.2.5\ninvalid entry\n2026-10-12T09:10:00Z unban 192.0.2.5\n"
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	before := fail2banLogPath
	fail2banLogPath = file
	defer func() { fail2banLogPath = before }()
	call := func(method string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/security/fail2ban", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := call("GET", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous access: %d", w.Code)
	}
	token, _, err := a.Login("127.0.0.1", "admin", "long secret passphrase")
	if err != nil {
		t.Fatal(err)
	}
	c := &http.Cookie{Name: "ups_bridge_session", Value: token}
	if w := call("POST", c); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("write method: %d", w.Code)
	}
	w := call("GET", c)
	if w.Code != http.StatusOK {
		t.Fatalf("read request: %d", w.Code)
	}
	var decoded struct {
		Events []securityEvent `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Events) != 2 || decoded.Events[0].Action != "unban" {
		t.Fatalf("unexpected events: %v", decoded.Events)
	}
	saved, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(saved)) != strings.TrimSpace(data) {
		t.Fatal("read-only endpoint changed log")
	}
}
