package protocol

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var fixtureTypes = map[string]struct{}{
	"agent.hello":     {},
	"terminal.closed": {},
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "protocol", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFixtures(t *testing.T) {
	for _, name := range []string{"agent-hello.json", "terminal-closed.json"} {
		if _, err := DecodeEnvelope(fixture(t, name), fixtureTypes); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := DecodeEnvelope(fixture(t, "invalid-duplicate.json"), fixtureTypes); err == nil {
		t.Fatal("duplicate envelope field was accepted")
	}
}

func TestFrames(t *testing.T) {
	raw, err := hex.DecodeString(strings.TrimSpace(string(fixture(t, "resize.hex"))))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodeFrame(raw)
	if err != nil || frame.Type != FrameResize {
		t.Fatalf("DecodeFrame() = %#v, %v", frame, err)
	}
	if _, err := DecodeFrame([]byte{FrameResize, 0, 0, 0, 0}); err == nil {
		t.Fatal("zero size was accepted")
	}
	if _, err := DecodeFrame([]byte{0xff}); err == nil {
		t.Fatal("unknown frame was accepted")
	}
}

type helloPayload struct {
	AgentVersion string `json:"agent_version"`
}

func TestPayloadRejectsUnknownField(t *testing.T) {
	if err := DecodePayload([]byte(`{"agent_version":"0.1.0","extra":true}`), &helloPayload{}); err == nil {
		t.Fatal("unknown payload field was accepted")
	}
}
