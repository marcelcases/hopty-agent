package terminal

import "testing"

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
