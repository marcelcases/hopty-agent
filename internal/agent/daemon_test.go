package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcelcases/hopty-agent/internal/localapi"
)

func TestDaemonStatusAndLock(t *testing.T) {
	home := t.TempDir()
	daemon, err := Start(home)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	if _, err := Start(home); err == nil {
		t.Fatal("second daemon acquired the lock")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx) }()

	var response localapi.Response
	for deadline := time.Now().Add(time.Second); ; {
		response, err = localapi.Call(daemon.SocketPath(), localapi.Request{Command: "status"})
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if response.Status == nil || response.Status.Connected || response.Status.Paired {
		t.Fatalf("unexpected status: %#v", response)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDaemonRefusesNonSocket(t *testing.T) {
	home := t.TempDir()
	runDirectory := filepath.Join(home, "run")
	if err := os.MkdirAll(runDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDirectory, "agent.sock"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(home); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatal("non-socket path was accepted")
	}
}
