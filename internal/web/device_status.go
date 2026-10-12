package web

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/power"
)

// deviceReachability contains no network endpoint, account name, key or action
// data. The dashboard needs only the device's configured friendly name and the
// result of a TCP connection to its SSH port.
type deviceReachability struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at"`
}

// probeSavedDevices limits simultaneous network connections; all probe results
// are assigned in the original saved-device order. It never executes SSH,
// shutdown, WoL or an authentication handshake.
func probeSavedDevices(ctx context.Context, devices []power.Device, probe func(context.Context, power.Device) bool) []deviceReachability {
	results := make([]deviceReachability, len(devices))
	if len(devices) == 0 {
		return results
	}
	var wg sync.WaitGroup
	limit := make(chan struct{}, 8)
	for i, dev := range devices {
		wg.Add(1)
		go func(i int, dev power.Device) {
			defer wg.Done()
			results[i] = deviceReachability{ID: dev.ID, Name: dev.Name, Status: "offline", CheckedAt: time.Now().UTC()}
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				return
			}
			if ctx.Err() == nil && probe(ctx, dev) {
				results[i].Status = "online"
			}
			results[i].CheckedAt = time.Now().UTC()
		}(i, dev)
	}
	wg.Wait()
	return results
}

func (h *Handler) deviceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.Power == nil {
		http.Error(w, "power controller unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	// Reachable is an unprivileged TCP dial, NOT SSH authentication.
	results := probeSavedDevices(ctx, h.Power.Config().Devices, (power.LinuxExecutor{}).Reachable)
	respondJSON(w, struct {
		Devices []deviceReachability `json:"devices"`
		Method  string               `json:"method"`
	}{Devices: results, Method: "ssh_tcp"})
}
