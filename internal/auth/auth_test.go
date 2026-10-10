package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPasswordInitializationLoginAndSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := Initialize(path, "a long and unique secret"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credentials file mode: %v", info.Mode())
	}
	content, _ := os.ReadFile(path)
	if string(content) == "a long and unique secret" {
		t.Fatal("plaintext password stored")
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Login("127.0.0.1", "admin", "wrong password"); err != ErrCredentials {
		t.Fatalf("wrong password accepted: %v", err)
	}
	token, csrf, err := store.Login("127.0.0.1", "admin", "a long and unique secret")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || csrf == "" || token == csrf {
		t.Fatal("invalid random session or csrf token")
	}
	if got, ok := store.Session(token); !ok || got != csrf {
		t.Fatal("valid session rejected")
	}
	if err = store.ChangePassword("a long and unique secret", "a newly rotated secret"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Session(token); ok {
		t.Fatal("password change did not revoke sessions")
	}
	if _, _, err = store.Login("127.0.0.1", "admin", "a newly rotated secret"); err != nil {
		t.Fatal(err)
	}
}
func TestShortPasswordRejected(t *testing.T) {
	if err := Initialize(filepath.Join(t.TempDir(), "auth.json"), "short"); err == nil {
		t.Fatal("short password accepted")
	}
}
func TestRateLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := Initialize(path, "a long and unique secret"); err != nil {
		t.Fatal(err)
	}
	a, _ := Open(path)
	for i := 0; i < 6; i++ {
		_, _, _ = a.Login("127.0.0.2", "nobody", "wrong")
	}
	if _, _, err := a.Login("127.0.0.2", "admin", "a long and unique secret"); err == nil {
		t.Fatal("rate limit bypass")
	}
}
