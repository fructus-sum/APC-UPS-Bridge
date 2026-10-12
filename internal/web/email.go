package web

import (
	"context"
	"html/template"
	"net/http"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/notify"
)

var emailPage = template.Must(template.ParseFS(static, "assets/email.html"))

func (h *Handler) emailRoutes(w http.ResponseWriter, r *http.Request, csrf string) bool {
	switch r.URL.Path {
	case "/email":
		if !requireMethod(w, r, http.MethodGet) {
			return true
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = emailPage.Execute(w, viewData{Page: "/email", CSRF: csrf})
		return true
	case "/assets/email.js":
		if !requireMethod(w, r, http.MethodGet) {
			return true
		}
		b, err := static.ReadFile("assets/email.js")
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(b)
		return true
	case "/api/email":
		if h.Mail == nil {
			http.Error(w, "email reporting unavailable", 503)
			return true
		}
		if r.Method == http.MethodGet {
			respondJSON(w, map[string]any{"settings": h.Mail.Config(), "status": h.Mail.View()})
			return true
		}
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		if !requireCSRF(w, r, csrf) {
			return true
		}
		var v notify.Config
		if err := decodeStrict(w, r, 4096, &v); err != nil {
			http.Error(w, bodyErrorText(err, "invalid SMTP settings"), 400)
			return true
		}
		if err := h.Mail.SaveConfig(v); err != nil {
			http.Error(w, err.Error(), 400)
			return true
		}
		respondJSON(w, map[string]any{"settings": h.Mail.Config(), "status": h.Mail.View()})
		return true
	case "/api/email/test":
		if h.Mail == nil {
			http.Error(w, "email reporting unavailable", 503)
			return true
		}
		if !requireMethod(w, r, http.MethodPost) {
			return true
		}
		if !requireCSRF(w, r, csrf) {
			return true
		}
		if !h.onlineConfirmed() {
			http.Error(w, "test email only available on verified mains power", 409)
			return true
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		if err := h.Mail.SendTest(ctx); err != nil {
			http.Error(w, "SMTP test failed; check account settings and connectivity", 502)
			return true
		}
		respondJSON(w, map[string]any{"message": "Test email sent"})
		return true
	}
	return false
}
