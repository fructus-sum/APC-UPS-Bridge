// Package web is the single-port, authenticated user interface for UPS Bridge.
package web

import (
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/auth"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/config"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
)

//go:embed assets/*
var static embed.FS

type Handler struct {
	Monitor *monitor.Monitor
	Auth    *auth.Store
	Config  *config.Store
	Logs    string
}
type viewData struct {
	Page    string
	CSRF    string
	Error   string
	Success string
}

var page = template.Must(template.ParseFS(static, "assets/app.html"))
var login = template.Must(template.ParseFS(static, "assets/login.html"))

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/login" {
		h.login(w, r)
		return
	}
	// Login styling is public; application HTML, JS and data still require login.
	if r.URL.Path == "/assets/app.css" && r.Method == http.MethodGet {
		b, err := static.ReadFile("assets/app.css")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(b)
		return
	}
	token, csrf, ok := h.session(r)
	if !ok {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "authentication required", http.StatusUnauthorized)
		} else {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		}
		return
	}
	switch r.URL.Path {
	case "/", "/events", "/settings":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, viewData{Page: r.URL.Path, CSRF: csrf})
	case "/assets/app.css", "/assets/app.js":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		b, err := static.ReadFile(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		}
		_, _ = w.Write(b)
	case "/api/status":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		respondJSON(w, h.Monitor.Snapshot())
	case "/api/events", "/api/events.csv", "/api/report":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		days, ok := queryInt(r, "days", 30, 0, 36500)
		if !ok {
			http.Error(w, "invalid days", 400)
			return
		}
		limit, ok := queryInt(r, "limit", 100, 1, 500)
		if !ok {
			http.Error(w, "invalid limit", 400)
			return
		}
		history, err := events.ReadHistory(h.Logs, days, limit, time.Now())
		if err != nil {
			http.Error(w, "failed to read event history", 500)
			return
		}
		if r.URL.Path == "/api/report" {
			respondJSON(w, history.Report)
		} else if r.URL.Path == "/api/events.csv" {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="apc-ups-bridge-events.csv"`)
			csvWriter := csv.NewWriter(w)
			_ = csvWriter.Write([]string{"timestamp_utc", "event", "level", "details_json"})
			for _, ev := range history.Events {
				details, _ := json.Marshal(ev.Details)
				_ = csvWriter.Write([]string{ev.Time.UTC().Format(time.RFC3339), excelSafe(ev.Type), excelSafe(ev.Level), excelSafe(string(details))})
			}
			csvWriter.Flush()
		} else {
			respondJSON(w, history.Events)
		}
	case "/api/settings":
		if r.Method == http.MethodGet {
			respondJSON(w, h.Config.Get())
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		if !csrfValid(r, csrf) {
			http.Error(w, "invalid CSRF token", 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var value config.Settings
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			http.Error(w, "invalid settings", 400)
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			http.Error(w, "unexpected extra data", 400)
			return
		}
		if err := h.Config.Save(value); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		respondJSON(w, h.Config.Get())
	case "/change-password":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		if !csrfValid(r, csrf) {
			http.Error(w, "invalid CSRF token", 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if err := h.Auth.ChangePassword(r.PostFormValue("old"), r.PostFormValue("new")); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(400)
			_ = page.Execute(w, viewData{Page: "/settings", CSRF: csrf, Error: "Current password incorrect or new password invalid (minimum 12 characters)."})
			return
		}
		h.Auth.Logout(token)
		clearCookie(w, r)
		http.Redirect(w, r, "/login?changed=1", http.StatusSeeOther)
	case "/logout":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		if !csrfValid(r, csrf) {
			http.Error(w, "invalid CSRF token", 403)
			return
		}
		h.Auth.Logout(token)
		clearCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}

// Prefix formula-like cells to prevent spreadsheet execution on CSV open.
func excelSafe(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}
func queryInt(r *http.Request, key string, defaultValue, min, max int) (int, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return defaultValue, true
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil && n >= min && n <= max
}
func respondJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
func (h *Handler) session(r *http.Request) (string, string, bool) {
	c, err := r.Cookie("ups_bridge_session")
	if err != nil {
		return "", "", false
	}
	csrf, ok := h.Auth.Session(c.Value)
	return c.Value, csrf, ok
}
func csrfValid(r *http.Request, expected string) bool {
	actual := r.Header.Get("X-CSRF-Token")
	if actual == "" {
		actual = r.PostFormValue("csrf")
	}
	return actual != "" && subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
func clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "ups_bridge_session", Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := viewData{}
		if r.URL.Query().Get("changed") == "1" {
			data.Success = "Password updated. Sign in again."
		}
		_ = login.Execute(w, data)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	addr, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		addr = r.RemoteAddr
	}
	token, _, err := h.Auth.Login(addr, r.PostFormValue("username"), r.PostFormValue("password"))
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if !errors.Is(err, auth.ErrCredentials) && strings.Contains(err.Error(), "too many") {
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
		_ = login.Execute(w, viewData{Error: "Invalid credentials or too many attempts. Try again later."})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "ups_bridge_session", Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: int((8 * time.Hour).Seconds())})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// New creates a handler with no listener and no background goroutines.
func New(mon *monitor.Monitor, a *auth.Store, c *config.Store, logs string) (http.Handler, error) {
	if mon == nil || a == nil || c == nil {
		return nil, fmt.Errorf("monitor, authentication and settings are required")
	}
	return &Handler{Monitor: mon, Auth: a, Config: c, Logs: logs}, nil
}
