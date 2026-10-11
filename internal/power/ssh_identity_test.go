package power

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServicePublicKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if keys := ServicePublicKeys(); keys == nil || len(keys) != 0 {
		t.Fatalf("expected empty non-nil slice, got %#v", keys)
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	// SSH wire format: string "ssh-ed25519" followed by string key bytes.
	blob := []byte{0, 0, 0, 11}
	blob = append(blob, "ssh-ed25519"...)
	blob = append(blob, 0, 0, 0, byte(len(pub)))
	blob = append(blob, pub...)
	line := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " bridge@example"
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Malformed files must be ignored.
	if err := os.WriteFile(filepath.Join(sshDir, "id_ecdsa.pub"), []byte("not a key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_rsa.pub"), []byte("ssh-rsa !!!notbase64!!!\nssh-rsa AAAA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keys := ServicePublicKeys()
	if len(keys) != 1 {
		t.Fatalf("expected one valid key, got %#v", keys)
	}
	sum := sha256.Sum256(blob)
	want := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	k := keys[0]
	if k.Type != "ssh-ed25519" || k.Key != line || k.Fingerprint != want || !strings.HasSuffix(k.Path, "id_ed25519.pub") {
		t.Fatalf("unexpected key: %#v", k)
	}
}

func TestServicePublicKeysRejectsOversizedAndMultiline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	big := "ssh-ed25519 " + strings.Repeat("A", 20000) + "\n"
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519.pub"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_rsa.pub"), []byte("ssh-rsa AAAA\nssh-rsa AAAA\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory with a key's name is not a key file.
	if err := os.Mkdir(filepath.Join(sshDir, "id_ecdsa.pub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if keys := ServicePublicKeys(); len(keys) != 0 {
		t.Fatalf("expected no keys, got %#v", keys)
	}
}
