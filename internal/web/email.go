package web

import (
	"context"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/notify"
)

var emailPage = template.Must(template.ParseFS(static, "assets/email.html"))

// AttachEmail adds email configuration/reporting to the existing authenticated
// single-port web handler. It does not create an additional listener.
func AttachEmail(h http.Handler, engine *notify.Engine) {
	if v, ok := h.(*Handler); ok {
		v.Mail = engine
	}
}
func (h *Handler) emailRoutes(w http.ResponseWriter, r *http.Request, csrf string) bool {
	switch r.URL.Path {
	case "/email":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return true
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = emailPage.Execute(w, viewData{Page: "/email", CSRF: csrf})
		return true
	case "/assets/email.js":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
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
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return true
		}
		if !csrfValid(r, csrf) {
			http.Error(w, "invalid CSRF token", 403)
			return true
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var v notify.Config
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&v); err != nil {
			http.Error(w, "invalid SMTP settings", 400)
			return true
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "unexpected extra data", 400)
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
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return true
		}
		if !csrfValid(r, csrf) {
			http.Error(w, "invalid CSRF token", 403)
			return true
		}
		s := h.Monitor.Snapshot()
		if !s.Healthy || s.Current == nil || s.Current.Power != apc.Online {
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
