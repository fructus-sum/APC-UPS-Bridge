package web

import (
	"bufio"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// authFailureLogFormat is the journal line Fail2ban matches (see
// packaging/fail2ban/filter.d/apc-ups-bridge.conf); change both together.
const authFailureLogFormat = "APC_UPS_BRIDGE_AUTH_FAILURE ip=%s"

// Independent privileged audit trail. Never make it writable by the web service.
// Deliberately read only the dedicated exported log, not general system journal.
const defaultFail2banLog = "/var/log/apc-ups-bridge-fail2ban/events.log"

type securityEvent struct {
	Time   string `json:"time"`
	Action string `json:"action"`
	IP     string `json:"ip"`
}

type securityResponse struct {
	Events  []securityEvent `json:"events"`
	Message string          `json:"message"`
}

func (h *Handler) securityRoutes(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/security/fail2ban" {
		return false
	}
	if !requireMethod(w, r, http.MethodGet) {
		return true
	}
	f, err := os.Open(h.Fail2banLog)
	if err != nil {
		if os.IsNotExist(err) {
			respondJSON(w, securityResponse{[]securityEvent{}, "Fail2ban event log is not available. Check the installation and service."})
		} else {
			http.Error(w, "Security event log unavailable", http.StatusServiceUnavailable)
		}
		return true
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 4096)
	recent := make([]securityEvent, 0, 100)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		if fields[1] != "ban" && fields[1] != "unban" {
			continue
		}
		if net.ParseIP(fields[2]) == nil {
			continue
		}
		if _, err := time.Parse(time.RFC3339, fields[0]); err != nil {
			continue
		}
		v := securityEvent{Time: fields[0], Action: fields[1], IP: fields[2]}
		if len(recent) == 100 {
			copy(recent, recent[1:])
			recent[99] = v
		} else {
			recent = append(recent, v)
		}
	}
	if err := scanner.Err(); err != nil {
		http.Error(w, "Security event log unreadable", http.StatusServiceUnavailable)
		return true
	}
	// Most recent first; none of these fields can come from a web request.
	for i, j := 0, len(recent)-1; i < j; i, j = i+1, j-1 {
		recent[i], recent[j] = recent[j], recent[i]
	}
	respondJSON(w, securityResponse{recent, "Read-only Fail2ban actions for the APC UPS Bridge jail."})
	return true
}
