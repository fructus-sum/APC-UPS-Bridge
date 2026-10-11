package power

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// SSHPublicKey describes a public key of the service user's SSH identity.
// Only .pub files are ever read; private keys are never opened.
type SSHPublicKey struct {
	Type        string `json:"type"`
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
	Path        string `json:"path"`
}

const maxPublicKeyFile = 16 * 1024

// ServicePublicKeys returns the valid public keys found in the current user's
// ~/.ssh directory, in the order ed25519, ecdsa, rsa. Missing or malformed
// files are ignored. The result is never nil.
func ServicePublicKeys() []SSHPublicKey {
	keys := []SSHPublicKey{}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// ssh itself uses the account's passwd home when HOME is unset.
		u, uerr := user.Current()
		if uerr != nil || u.HomeDir == "" {
			return keys
		}
		home = u.HomeDir
	}
	for _, name := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
		path := filepath.Join(home, ".ssh", name)
		if key, ok := readPublicKey(path); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

func readPublicKey(path string) (SSHPublicKey, bool) {
	// Only regular files: a directory or FIFO with a key's name is ignored
	// rather than blocking the request.
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return SSHPublicKey{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return SSHPublicKey{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPublicKeyFile+1))
	if err != nil || len(data) > maxPublicKeyFile {
		return SSHPublicKey{}, false
	}
	line := strings.TrimRight(string(data), "\r\n")
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return SSHPublicKey{}, false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return SSHPublicKey{}, false
	}
	typ := fields[0]
	if typ != "ssh-ed25519" && typ != "ssh-rsa" && !strings.HasPrefix(typ, "ecdsa-sha2-") && !strings.HasPrefix(typ, "sk-") {
		return SSHPublicKey{}, false
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(blob) == 0 {
		return SSHPublicKey{}, false
	}
	sum := sha256.Sum256(blob)
	return SSHPublicKey{
		Type:        typ,
		Key:         line,
		Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:]),
		Path:        path,
	}, true
}
