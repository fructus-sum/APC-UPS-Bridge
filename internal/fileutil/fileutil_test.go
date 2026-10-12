package fileutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileAtomicCreatesDirAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "f.json")
	if err := WriteFileAtomic(path, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "two" {
		t.Fatalf("got %q, %v", b, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0600 {
			t.Fatalf("mode %v", st.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestWriteFileAtomicCleansUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	// Renaming a file over a directory fails after the temp file was written.
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "x"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(target, []byte("data"), 0600); err == nil {
		t.Fatal("expected error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestWriteFileAtomicMkdirFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(filepath.Join(blocker, "f"), []byte("x"), 0600); err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteJSONAtomicFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	if err := WriteJSONAtomic(path, map[string]int{"a": 1}, 0600); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{\n  \"a\": 1\n}\n" {
		t.Fatalf("got %q", b)
	}
	if err := WriteJSONAtomic(path, make(chan int), 0600); err == nil {
		t.Fatal("expected marshal error")
	}
	b2, _ := os.ReadFile(path)
	if string(b2) != string(b) {
		t.Fatal("file changed after failed marshal")
	}
}

func TestReadJSON(t *testing.T) {
	dir := t.TempDir()
	var v struct{ A int }
	found, err := ReadJSON(filepath.Join(dir, "missing"), &v)
	if found || err != nil {
		t.Fatalf("missing: %v %v", found, err)
	}
	good := filepath.Join(dir, "good")
	os.WriteFile(good, []byte(`{"A":7}`), 0600)
	found, err = ReadJSON(good, &v)
	if !found || err != nil || v.A != 7 {
		t.Fatalf("good: %v %v %+v", found, err, v)
	}
	bad := filepath.Join(dir, "bad")
	os.WriteFile(bad, []byte(`{`), 0600)
	if found, err = ReadJSON(bad, &v); !found || err == nil {
		t.Fatalf("bad: %v %v", found, err)
	}
	if _, err = ReadJSON(dir, &v); err == nil {
		t.Fatal("expected read error for directory")
	}
}
