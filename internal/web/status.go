package web

import (
	"net/http"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

// AttachStatus adds the device online-status monitor to the handler.
func AttachStatus(h http.Handler, m *power.StatusMonitor) {
	if v, ok := h.(*Handler); ok {
		v.Status = m
	}
}

// statusRoutes serves /api/power/status. It only reads the monitor's memory;
// it never starts a network check.
func (h *Handler) statusRoutes(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/power/status" {
		return false
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return true
	}
	devices := []power.DeviceStatus{}
	if h.Status != nil {
		devices = h.Status.Snapshot()
	}
	respondJSON(w, struct {
		IntervalSeconds int                  `json:"interval_seconds"`
		Devices         []power.DeviceStatus `json:"devices"`
	}{int(power.StatusInterval.Seconds()), devices})
	return true
}
