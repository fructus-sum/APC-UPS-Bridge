// Package web is the single-port, authenticated user interface for UPS Bridge.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/auth"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/config"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/firmware"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/history"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/hostsettings"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/notify"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

//go:embed assets/*
var static embed.FS

type Handler struct {
	Monitor *monitor.Monitor
	Auth    *auth.Store
	Config  *config.Store
	Logs    string
	Power   *power.Controller
	Mail    *notify.Engine
	History *history.Store
	Status  *power.StatusMonitor
	// Fail2banLog is the exported Fail2ban event log shown on the security page.
	Fail2banLog string
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
	if h.securityRoutes(w, r) {
		return
	}
	if h.historyRoutes(w, r) {
		return
	}
	if h.emailRoutes(w, r, csrf) {
		return
	}
	if h.statusRoutes(w, r) {
		return
	}
	switch r.URL.Path {
	case "/", "/events", "/settings", "/power", "/history", "/security":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = page.Execute(w, viewData{Page: r.URL.Path, CSRF: csrf})
	case "/assets/app.css":
		// GET is served before the session check; other methods are refused.
		requireMethod(w, r, http.MethodGet)
	case "/assets/app.js", "/assets/common.js":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		b, err := static.ReadFile(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(b)
	case "/api/status":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		respondJSON(w, h.Monitor.Snapshot())
	case "/api/firmware":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		status := h.Monitor.Snapshot()
		respondJSON(w, firmware.Check(status.Current))
	case "/api/events", "/api/events.csv", "/api/report":
		h.eventsRoute(w, r)
	case "/api/settings":
		h.settingsRoute(w, r, csrf)
	case "/api/power":
		h.powerConfigRoute(w, r, csrf)
	case "/api/power/host-settings":
		h.hostSettingsRoute(w, r, csrf)
	case "/api/power/host-thresholds":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		respondJSON(w, hostShutdownSettings())
	case "/api/power/test-ssh":
		h.testSSHRoute(w, r, csrf)
	case "/api/power/ssh-key":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		respondJSON(w, struct {
			Keys []power.SSHPublicKey `json:"keys"`
		}{power.ServicePublicKeys()})
	case "/api/power/state":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		if h.Power == nil {
			http.Error(w, "power automation unavailable", http.StatusServiceUnavailable)
			return
		}
		respondJSON(w, h.Power.View())
	case "/change-password":
		h.changePasswordRoute(w, r, token, csrf)
	case "/logout":
		if !requireMethod(w, r, http.MethodPost) || !requireCSRF(w, r, csrf) {
			return
		}
		h.Auth.Logout(token)
		setSessionCookie(w, r, "", -1)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}

// eventsRoute serves the event list, its CSV export and the summary report.
func (h *Handler) eventsRoute(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
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
	hist, err := events.ReadHistory(h.Logs, days, limit, time.Now())
	if err != nil {
		http.Error(w, "failed to read event history", 500)
		return
	}
	switch r.URL.Path {
	case "/api/report":
		respondJSON(w, hist.Report)
	case "/api/events.csv":
		writeEventsCSV(w, hist.Events)
	default:
		respondJSON(w, hist.Events)
	}
}

func writeEventsCSV(w http.ResponseWriter, evs []events.Event) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="apc-ups-bridge-events.csv"`)
	csvWriter := csv.NewWriter(w)
	_ = csvWriter.Write([]string{"timestamp_utc", "event", "level", "details_json"})
	for _, ev := range evs {
		details, _ := json.Marshal(ev.Details)
		_ = csvWriter.Write([]string{ev.Time.UTC().Format(time.RFC3339), excelSafe(ev.Type), excelSafe(ev.Level), excelSafe(string(details))})
	}
	csvWriter.Flush()
}

func (h *Handler) settingsRoute(w http.ResponseWriter, r *http.Request, csrf string) {
	if r.Method == http.MethodGet {
		respondJSON(w, h.Config.Get())
		return
	}
	if !requireMethod(w, r, http.MethodPost) || !requireCSRF(w, r, csrf) {
		return
	}
	var value config.Settings
	if err := decodeStrict(w, r, 4096, &value); err != nil {
		http.Error(w, bodyErrorText(err, "invalid settings"), 400)
		return
	}
	if err := h.Config.Save(value); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	respondJSON(w, h.Config.Get())
}

func (h *Handler) powerConfigRoute(w http.ResponseWriter, r *http.Request, csrf string) {
	if h.Power == nil {
		http.Error(w, "power automation unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method == http.MethodGet {
		respondJSON(w, h.Power.Config())
		return
	}
	if !requireMethod(w, r, http.MethodPost) || !requireCSRF(w, r, csrf) {
		return
	}
	var value power.Config
	if err := decodeStrict(w, r, 32768, &value); err != nil {
		http.Error(w, bodyErrorText(err, "invalid power rules"), 400)
		return
	}
	if err := h.Power.UpdateConfig(value); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if h.Status != nil {
		h.Status.Nudge()
	}
	respondJSON(w, h.Power.Config())
}

func (h *Handler) hostSettingsRoute(w http.ResponseWriter, r *http.Request, csrf string) {
	if !requireMethod(w, r, http.MethodPost) || !requireCSRF(w, r, csrf) {
		return
	}
	var v hostsettings.Settings
	if decodeStrict(w, r, 1024, &v) != nil {
		http.Error(w, "invalid shutdown settings", 400)
		return
	}
	if err := v.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if v.Managed && !h.onlineConfirmed() {
		http.Error(w, "UPS is not confirmed ONLINE; shutdown settings cannot be changed", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(55 * time.Second))
	result, err := applyHostSettings(ctx, v)
	if err != nil {
		http.Error(w, "Local apcupsd settings service unavailable: "+err.Error(), 503)
		return
	}
	if result.Error != "" {
		http.Error(w, result.Error, 409)
		return
	}
	respondJSON(w, result)
}

func (h *Handler) testSSHRoute(w http.ResponseWriter, r *http.Request, csrf string) {
	if !requireMethod(w, r, http.MethodPost) || !requireCSRF(w, r, csrf) {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if decodeStrict(w, r, 1024, &req) != nil || req.ID == "" {
		http.Error(w, "invalid saved device ID", 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, found := h.testSavedSSH(ctx, req.ID)
	if !found {
		http.Error(w, "saved device not found", 404)
		return
	}
	respondJSON(w, result)
}

func (h *Handler) changePasswordRoute(w http.ResponseWriter, r *http.Request, token, csrf string) {
	if !requireMethod(w, r, http.MethodPost) || !requireCSRF(w, r, csrf) {
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
	setSessionCookie(w, r, "", -1)
	http.Redirect(w, r, "/login?changed=1", http.StatusSeeOther)
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
	c, err := r.Cookie(sessionCookieName)
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

// setSessionCookie sets (or, with maxAge -1, clears) the session cookie.
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: value, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
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
	if !requireMethod(w, r, http.MethodPost) {
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
		if ip := net.ParseIP(addr); ip != nil && errors.Is(err, auth.ErrCredentials) {
			log.Printf(authFailureLogFormat, ip.String())
		}
		if errors.Is(err, auth.ErrSuspicious) && h.Mail != nil {
			go h.Mail.SendSecurityWarning()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if !errors.Is(err, auth.ErrCredentials) && strings.Contains(err.Error(), "too many") {
			w.WriteHeader(http.StatusTooManyRequests)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
		_ = login.Execute(w, viewData{Error: "Invalid credentials or too many attempts. Try again later."})
		return
	}
	setSessionCookie(w, r, token, int(sessionLifetime.Seconds()))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// New creates a handler with no listener and no background goroutines.
func New(mon *monitor.Monitor, a *auth.Store, c *config.Store, logs string, p *power.Controller) (*Handler, error) {
	if mon == nil || a == nil || c == nil {
		return nil, fmt.Errorf("monitor, authentication and settings are required")
	}
	return &Handler{Monitor: mon, Auth: a, Config: c, Logs: logs, Power: p, Fail2banLog: defaultFail2banLog}, nil
}

const (
	sessionCookieName = "ups_bridge_session"
	sessionLifetime   = 8 * time.Hour
)

// requireMethod answers 405 and returns false unless the request uses method.
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// requireCSRF answers 403 and returns false unless the request carries csrf.
func requireCSRF(w http.ResponseWriter, r *http.Request, csrf string) bool {
	if !csrfValid(r, csrf) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return false
	}
	return true
}

var errTrailingData = errors.New("unexpected extra data")

// decodeStrict reads one JSON value of at most limit bytes into dst, rejecting
// unknown fields and anything after the value. Callers word their own 400
// reply; bodyErrorText gives the usual one.
func decodeStrict(w http.ResponseWriter, r *http.Request, limit int64, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errTrailingData
	}
	return nil
}

// bodyErrorText is the 400 text for a decodeStrict failure: invalid when the
// value itself was bad, a fixed message when extra data followed it.
func bodyErrorText(err error, invalid string) string {
	if errors.Is(err, errTrailingData) {
		return errTrailingData.Error()
	}
	return invalid
}

// onlineConfirmed reports whether the UPS is healthy and running on mains power.
func (h *Handler) onlineConfirmed() bool {
	s := h.Monitor.Snapshot()
	return s.Healthy && s.Current != nil && s.Current.Power == apc.Online
}
