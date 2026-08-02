package terminal

import (
	"encoding/json"
	"testing"
)

func TestCloseReasonAcceptsLeaseExpiry(t *testing.T) {
	if reason := closeReason(json.RawMessage(`{"reason":"lease_expired"}`)); reason != "lease_expired" {
		t.Fatalf("reason = %q", reason)
	}
	if reason := closeReason(json.RawMessage(`{"reason":""}`)); reason != "browser_closed" {
		t.Fatalf("empty reason = %q", reason)
	}
}

func TestTerminalEnvironmentSetsTerminalCapabilities(t *testing.T) {
	environment := terminalEnvironment([]string{"PATH=/usr/bin", "TERM=dumb", "COLORTERM=none", "TERM_PROGRAM=other"})
	want := map[string]bool{"PATH=/usr/bin": true, "TERM=xterm-256color": true, "COLORTERM=truecolor": true, "TERM_PROGRAM=hopty": true}
	if len(environment) != len(want) {
		t.Fatalf("environment = %#v", environment)
	}
	for _, variable := range environment {
		if !want[variable] {
			t.Fatalf("unexpected variable %q in %#v", variable, environment)
		}
	}
}
