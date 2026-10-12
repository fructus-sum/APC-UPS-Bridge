// Package auth provides local administrator authentication with salted PBKDF2,
// short-lived random sessions and bounded in-memory login rate limiting.
// This is deliberately standard-library-only and has no external identity service.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const iterations = 240000
const sessionLifetime = 8 * time.Hour

var ErrCredentials = errors.New("invalid credentials")

// These match the Fail2ban jail (maxretry=5, findtime=10m in
// scripts/setup-fail2ban.sh); change them together.
const (
	suspiciousFailures = 5
	attemptWindow      = 10 * time.Minute
)

type suspiciousError struct{}

func (suspiciousError) Error() string { return "suspected attack after five failed logins" }

// Unwrap lets callers treat the fifth failure as an ordinary credential failure.
func (suspiciousError) Unwrap() error { return ErrCredentials }

var ErrSuspicious error = suspiciousError{}

type record struct {
	Scheme     string `json:"scheme"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	Hash       string `json:"hash"`
}
type session struct {
	csrf  string
	until time.Time
}
type attempt struct {
	count int
	since time.Time
}
type Store struct {
	mu       sync.Mutex
	path     string
	password record
	sessions map[[32]byte]session
	attempts map[string]attempt
	now      func() time.Time
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, sessions: make(map[[32]byte]session), attempts: make(map[string]attempt), now: time.Now}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("admin password not initialized: %w", err)
	}
	if err = json.Unmarshal(data, &s.password); err != nil {
		return nil, fmt.Errorf("invalid password store: %w", err)
	}
	if s.password.Scheme != "pbkdf2-sha256" || s.password.Iterations < 100000 || s.password.Iterations > 1000000 {
		return nil, errors.New("unsupported password hash settings")
	}
	if _, err = base64.RawStdEncoding.DecodeString(s.password.Salt); err != nil {
		return nil, errors.New("invalid password salt")
	}
	if _, err = base64.RawStdEncoding.DecodeString(s.password.Hash); err != nil {
		return nil, errors.New("invalid password digest")
	}
	return s, nil
}
func Initialize(path, password string) error { return writePassword(path, password) }
func writePassword(path, password string) error {
	if len(password) < 12 || len(password) > 256 {
		return errors.New("password must be 12–256 bytes")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	digest := pbkdf2([]byte(password), salt, iterations, 32)
	rec := record{Scheme: "pbkdf2-sha256", Iterations: iterations, Salt: base64.RawStdEncoding.EncodeToString(salt), Hash: base64.RawStdEncoding.EncodeToString(digest)}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".auth-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func pbkdf2(password, salt []byte, count, keyLen int) []byte {
	// RFC 8018 PBKDF2-HMAC-SHA256 (one 32-byte block for this application).
	var key []byte
	for block := uint32(1); len(key) < keyLen; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < count; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		key = append(key, t...)
	}
	return key[:keyLen]
}
func verify(r record, password string) bool {
	salt, err1 := base64.RawStdEncoding.DecodeString(r.Salt)
	expected, err2 := base64.RawStdEncoding.DecodeString(r.Hash)
	if err1 != nil || err2 != nil || len(expected) != 32 {
		return false
	}
	hash := pbkdf2([]byte(password), salt, r.Iterations, 32)
	return subtle.ConstantTimeCompare(hash, expected) == 1
}
func randomString() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
func (s *Store) Login(ip, username, password string) (token, csrf string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.prune(now)
	if len(s.attempts) > 2048 { // bounded map under hostile traffic
		for k := range s.attempts {
			delete(s.attempts, k)
		}
	}
	a := s.attempts[ip]
	if now.Sub(a.since) > attemptWindow {
		a = attempt{since: now}
	}
	if a.count >= 6 {
		return "", "", errors.New("too many attempts; retry in five minutes")
	}
	if username != "admin" || !verify(s.password, password) {
		a.count++
		s.attempts[ip] = a
		if a.count == suspiciousFailures {
			return "", "", ErrSuspicious
		}
		return "", "", ErrCredentials
	}
	delete(s.attempts, ip)
	if len(s.sessions) >= 256 {
		return "", "", errors.New("too many active sessions")
	}
	token, err = randomString()
	if err != nil {
		return "", "", err
	}
	csrf, err = randomString()
	if err != nil {
		return "", "", err
	}
	s.sessions[sha256.Sum256([]byte(token))] = session{csrf: csrf, until: now.Add(sessionLifetime)}
	return token, csrf, nil
}
func (s *Store) prune(now time.Time) {
	for k, v := range s.sessions {
		if !now.Before(v.until) {
			delete(s.sessions, k)
		}
	}
	for k, v := range s.attempts {
		if now.Sub(v.since) > attemptWindow {
			delete(s.attempts, k)
		}
	}
}
func (s *Store) Session(token string) (string, bool) {
	if len(token) > 128 || token == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.sessions[sha256.Sum256([]byte(token))]
	if !ok {
		return "", false
	}
	if !s.now().Before(entry.until) {
		delete(s.sessions, sha256.Sum256([]byte(token)))
		return "", false
	}
	return entry.csrf, true
}
func (s *Store) Logout(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sha256.Sum256([]byte(token)))
}
func (s *Store) ChangePassword(oldPwd, newPwd string) error {
	if len(newPwd) < 12 || len(newPwd) > 256 {
		return errors.New("new password must be 12–256 bytes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !verify(s.password, oldPwd) {
		return ErrCredentials
	}
	if err := writePassword(s.path, newPwd); err != nil {
		return err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &s.password); err != nil {
		return err
	}
	clear(s.sessions)
	return nil
}
func SafeIP(remote string) string { // ignore X-Forwarded-For (untrusted)
	if idx := strings.LastIndex(remote, ":"); idx >= 0 {
		return remote[:idx]
	}
	return remote
}
