package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marcelcases/hopty-agent/internal/localapi"
)

func TestVersion(t *testing.T) {
	if err := run([]string{"--version"}); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsCommand(t *testing.T) {
	if err := run([]string{"pair"}); err == nil {
		t.Fatal("run accepted an unavailable command")
	}
}

func TestPrintStatusUnpaired(t *testing.T) {
	output := captureOutput(t, func() {
		printStatus(localapi.Status{AgentVersion: "v0.1.0-beta.1"})
	})
	want := "Hopty Agent v0.1.0-beta.1 is installed.\n\nTo create a passkey, run hopty pair\nTo uninstall, run hopty uninstall\n"
	if output != want {
		t.Fatalf("status output = %q, want %q", output, want)
	}
}

func TestPrintStatusPairedWithoutSessions(t *testing.T) {
	last := time.Date(2026, time.July, 8, 19, 8, 22, 0, time.UTC)
	created := time.Date(2026, time.July, 10, 10, 30, 14, 0, time.UTC)
	output := captureOutput(t, func() {
		printStatus(localapi.Status{AgentVersion: "v0.1.0-beta.1", Paired: true, LastAccessAt: &last, PasskeyCreatedAt: &created})
	})
	for _, field := range []string{
		"Hopty Agent v0.1.0-beta.1 is up.",
		"No active sessions.",
		"Go to hopty.net to access your terminal",
		"Last access         2026-07-08 19:08:22 UTC",
		"Passkey created     2026-07-10 10:30:14 UTC",
		"To revoke passkey, run hopty revoke",
		"To uninstall, run hopty uninstall",
	} {
		if !strings.Contains(output, field) {
			t.Fatalf("status output %q does not contain %q", output, field)
		}
	}
}

func TestPrintStatusWithSession(t *testing.T) {
	output := captureOutput(t, func() {
		printStatus(localapi.Status{AgentVersion: "v0.1.0-beta.1", Paired: true, Sessions: []localapi.SessionStatus{{User: "user@host", Connection: "direct", Transport: "WebRTC", LatencyMS: 5, IncomingIP: "203.0.113.7"}}})
	})
	for _, field := range []string{
		"Hopty Agent v0.1.0-beta.1 is up.",
		"Active sessions     1",
		"User                user@host",
		"Connection          direct",
		"Transport           WebRTC",
		"Latency             5 ms",
		"Incoming IP         203.0.113.7",
		"To revoke passkey, run hopty revoke",
	} {
		if !strings.Contains(output, field) {
			t.Fatalf("status output %q does not contain %q", output, field)
		}
	}
	if strings.Contains(output, "Go to hopty.net") {
		t.Fatal("active status should not print the no-session landing message")
	}
}

func captureOutput(t *testing.T, callback func()) string {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	callback()
	_ = writer.Close()
	os.Stdout = original
	output, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}
