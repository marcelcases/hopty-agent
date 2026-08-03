package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestParseInstallerRelease(t *testing.T) {
	script := []byte("#!/bin/sh\nHOPTY_VERSION='v0.1.0-beta.3'\nHOPTY_SHA256_AMD64='" + validTestChecksum('a') + "'\nHOPTY_SHA256_ARM64='" + validTestChecksum('b') + "'\n")
	release, err := parseInstallerRelease(script)
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "v0.1.0-beta.3" || release.SHA256AMD64 != validTestChecksum('a') || release.SHA256ARM64 != validTestChecksum('b') {
		t.Fatalf("unexpected release metadata: %+v", release)
	}
}

func TestParseInstallerReleaseRejectsHTML(t *testing.T) {
	if _, err := parseInstallerRelease([]byte("<!doctype html>")); err == nil {
		t.Fatal("HTML installer response was accepted")
	}
}

func TestUpgradeExecutesLatestInstallerWithoutPairing(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(home, "upgrade-marker")
	script := []byte("#!/bin/sh\nHOPTY_VERSION='v0.1.0-beta.3'\nHOPTY_SHA256_AMD64='" + validTestChecksum('a') + "'\nHOPTY_SHA256_ARM64='" + validTestChecksum('b') + "'\nprintf '%s' \"$HOPTY_UPGRADE\" > \"$HOPTY_HOME/upgrade-marker\"\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != upgradeEndpointPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(script)
	}))
	defer server.Close()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("service_url = \""+server.URL+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	previousVersion := version
	version = "v0.1.0-beta.2"
	defer func() { version = previousVersion }()
	if err := upgradeWithClient(home, server.Client()); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != "1" {
		t.Fatalf("upgrade marker = %q, want 1", value)
	}
}

func TestUpgradeReportsCurrentVersion(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != upgradeEndpointPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("#!/bin/sh\nHOPTY_VERSION='v0.1.0-beta.2'\nHOPTY_SHA256_AMD64='" + validTestChecksum('a') + "'\nHOPTY_SHA256_ARM64='" + validTestChecksum('b') + "'\n"))
	}))
	defer server.Close()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("service_url = \""+server.URL+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previousVersion := version
	version = "v0.1.0-beta.2"
	defer func() { version = previousVersion }()

	if err := upgradeWithClient(home, server.Client()); err != nil {
		t.Fatal(err)
	}
}

func TestFetchLatestInstallerUsesServiceOrigin(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != upgradeEndpointPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("#!/bin/sh\nHOPTY_VERSION='v1'\nHOPTY_SHA256_AMD64='" + validTestChecksum('a') + "'\nHOPTY_SHA256_ARM64='" + validTestChecksum('b') + "'\n"))
	}))
	defer server.Close()
	serviceURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := fetchLatestInstaller(server.Client(), serviceURL)
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "v1" {
		t.Fatalf("release version = %q", release.Version)
	}
}

func TestVersionsMatchIgnoresPrefix(t *testing.T) {
	if !versionsMatch("0.1.0", "v0.1.0") {
		t.Fatal("version prefixes should not affect comparison")
	}
	if versionsMatch("v0.1.0", "v0.1.1") {
		t.Fatal("different versions were treated as equal")
	}
}

func validTestChecksum(character byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = character
	}
	return string(value)
}
