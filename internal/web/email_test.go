package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/notify"
)

type mailSender struct{}

func (mailSender) Send(context.Context, notify.Config, string, string, string) error { return nil }
func TestEmailPageAuthenticatedAndCredentialsHidden(t *testing.T) {
	pass := "very secret smtp token"
	cfg := notify.Config{Enabled: true, Host: "mail.example.net", Port: 465, Security: "tls", Username: "alert@example.net", Password: pass, From: "alert@example.net", To: "owner@example.net", WaitForDevices: true}
	f := newFixture(t, withMail(cfg))
	h, cookie, csrf := f.Handler, f.Cookie, f.CSRF
	if w := request(h, "GET", "/api/email", "", nil, ""); w.Code != 401 {
		t.Fatalf("email settings not protected: %d", w.Code)
	}
	if w := request(h, "GET", "/email", "", nil, ""); w.Code != 303 {
		t.Fatalf("email page not protected: %d", w.Code)
	}
	if w := request(h, "GET", "/assets/email.js", "", nil, ""); w.Code != 303 {
		t.Fatalf("email JS not protected: %d", w.Code)
	}
	w := request(h, "GET", "/api/email", "", cookie, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), pass) || !strings.Contains(w.Body.String(), "has_password") {
		t.Fatalf("email secret disclosure or failed response: %d %s", w.Code, w.Body.String())
	}
	cfg.Password = ""
	cfg.To = "newowner@example.net"
	body, _ := json.Marshal(cfg)
	if w := request(h, "POST", "/api/email", string(body), cookie, ""); w.Code != 403 {
		t.Fatalf("CSRF not checked: %d", w.Code)
	}
	w = request(h, "POST", "/api/email", string(body), cookie, csrf)
	if w.Code != 200 || strings.Contains(w.Body.String(), pass) {
		t.Fatalf("email save failed or leaked: %d %s", w.Code, w.Body.String())
	}
	if f.MailStore.Get().Password != pass {
		t.Fatal("blank password replaced existing secret")
	}
	if w := request(h, "GET", "/email", "", cookie, ""); w.Code != 200 {
		t.Fatalf("email page unavailable: %d", w.Code)
	}
}
