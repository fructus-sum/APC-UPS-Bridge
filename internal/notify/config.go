// Package notify implements durable, low-traffic UPS outage email notification.
package notify

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"strings"
	"sync"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/fileutil"
)

// Config is stored in a permission-restricted file. Password is never serialized
// to an HTTP response; callers must use View instead.
type Config struct {
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Security       string `json:"security"` // tls for 465; starttls for 587 or custom port
	Username       string `json:"username"`
	Password       string `json:"password,omitempty"`
	From           string `json:"from"`
	To             string `json:"to"`
	WaitForDevices bool   `json:"wait_for_devices"`
}
type View struct {
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Security       string `json:"security"`
	Username       string `json:"username"`
	HasPassword    bool   `json:"has_password"`
	From           string `json:"from"`
	To             string `json:"to"`
	WaitForDevices bool   `json:"wait_for_devices"`
}

func (c Config) View() View {
	return View{c.Enabled, c.Host, c.Port, c.Security, c.Username, c.Password != "", c.From, c.To, c.WaitForDevices}
}
func DefaultConfig() Config { return Config{Port: 587, Security: "starttls", WaitForDevices: true} }
func (c Config) Validate() error {
	if !c.Enabled && c.Host == "" {
		return nil
	}
	if len(c.Host) > 253 || strings.TrimSpace(c.Host) != c.Host || c.Host == "" || strings.ContainsAny(c.Host, " \r\n\t/@") {
		return errors.New("invalid SMTP hostname")
	}
	if net.ParseIP(c.Host) == nil {
		for _, part := range strings.Split(c.Host, ".") {
			if part == "" || len(part) > 63 {
				return errors.New("invalid SMTP hostname")
			}
		}
	}
	if c.Port < 1 || c.Port > 65535 || (c.Security != "tls" && c.Security != "starttls") {
		return errors.New("SMTP needs explicit TLS or STARTTLS and a valid port")
	}
	if len(c.Username) > 320 || strings.ContainsAny(c.Username, "\r\n") {
		return errors.New("invalid SMTP username")
	}
	if c.Enabled && (c.Username == "" || c.Password == "") {
		return errors.New("SMTP username and password are required")
	}
	if len(c.Password) > 1024 {
		return errors.New("SMTP password too long")
	}
	if c.Enabled || c.From != "" || c.To != "" {
		for _, s := range []string{c.From, c.To} {
			a, err := mail.ParseAddress(s)
			if err != nil || a.Address != s || strings.ContainsAny(s, "\r\n") {
				return fmt.Errorf("from/to must each be a single plain email address")
			}
		}
	}
	return nil
}

// Store persists SMTP settings separately from general application settings.
type Store struct {
	mu    sync.RWMutex
	path  string
	value Config
}

func OpenConfig(path string) (*Store, error) {
	s := &Store{path: path, value: DefaultConfig()}
	found, err := fileutil.ReadJSON(path, &s.value)
	if !found && err == nil {
		return s, nil
	}
	if err != nil {
		if found {
			err = fmt.Errorf("SMTP configuration invalid: %w", err)
		}
		return nil, err
	}
	if err = s.value.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) Get() Config { s.mu.RLock(); defer s.mu.RUnlock(); return s.value }

// Save treats a blank submitted password as 'keep existing password'.
func (s *Store) Save(v Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.Password == "" {
		v.Password = s.value.Password
	}
	if err := v.Validate(); err != nil {
		return err
	}
	if err := fileutil.WriteJSONAtomic(s.path, v, 0600); err != nil {
		return err
	}
	s.value = v
	return nil
}
