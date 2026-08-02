package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, FileName), []byte("service_url = \"https://api.hopty.net\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if config.ServiceURL.String() != "https://api.hopty.net" {
		t.Fatalf("unexpected service URL %q", config.ServiceURL)
	}
}

func TestLoadRejectsUnsafeOrigin(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, FileName), []byte("service_url = \"http://example.test/path\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(home); err == nil {
		t.Fatal("unsafe service URL was accepted")
	}
}
