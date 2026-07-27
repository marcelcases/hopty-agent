package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreatePreservesIdentity(t *testing.T) {
	directory := t.TempDir()
	first, err := LoadOrCreate(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(directory)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.PublicKey) != string(second.PublicKey) {
		t.Fatal("identity changed after restart")
	}
	info, err := os.Stat(filepath.Join(directory, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o", info.Mode().Perm())
	}
}

func TestRejectsInsecureKeyFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, keyFile)
	if err := os.WriteFile(path, make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(directory); err == nil {
		t.Fatal("group/world-readable key was accepted")
	}
}

func TestRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, keyFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(directory); err == nil {
		t.Fatal("symlinked key was accepted")
	}
}
