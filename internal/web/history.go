package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/history"
)

// AttachHistory keeps all graph endpoints inside the existing authenticated web server.
func AttachHistory(h http.Handler, store *history.Store) {
	if v, ok := h.(*Handler); ok {
		v.History = store
	}
}

func (h *Handler) historyRoutes(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/assets/history.js":
		if !requireMethod(w, r, http.MethodGet) {
			return true
		}
		b, err := static.ReadFile("assets/history.js")
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(b)
		return true
	case "/api/history/battery":
		if !requireMethod(w, r, http.MethodGet) {
			return true
		}
		if h.History == nil {
			http.Error(w, "history unavailable", http.StatusServiceUnavailable)
			return true
		}
		days, ok := queryDays(w, r, history.DefaultBatteryDays)
		if !ok {
			return true
		}
		if !history.ValidBatteryDays(days) {
			http.Error(w, "invalid range", http.StatusBadRequest)
			return true
		}
		report, err := h.History.ReadBattery(days, time.Now(), h.Logs)
		if err != nil {
			http.Error(w, "battery history unavailable", http.StatusInternalServerError)
			return true
		}
		respondJSON(w, report)
		return true
	case "/api/history":
		if !requireMethod(w, r, http.MethodGet) {
			return true
		}
		if h.History == nil {
			http.Error(w, "history unavailable", http.StatusServiceUnavailable)
			return true
		}
		days, ok := queryDays(w, r, history.DefaultDays)
		if !ok {
			return true
		}
		report, err := h.History.Read(days, time.Now(), h.Logs)
		if err != nil {
			if days != 1 && days != 7 && days != 14 && days != 30 {
				http.Error(w, "invalid range", http.StatusBadRequest)
			} else {
				http.Error(w, "history unavailable", http.StatusInternalServerError)
			}
			return true
		}
		respondJSON(w, report)
		return true
	}
	return false
}

// queryDays reads the optional "days" query value. A malformed value is
// answered with 400 and ok is false.
func queryDays(w http.ResponseWriter, r *http.Request, def int) (days int, ok bool) {
	raw := r.URL.Query().Get("days")
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		http.Error(w, "invalid range", http.StatusBadRequest)
		return 0, false
	}
	return n, true
}
