package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/marcelcases/hopty-agent/internal/agent"
	"github.com/marcelcases/hopty-agent/internal/localapi"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "hopty:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println(version)
		return nil
	}
	if len(args) == 0 {
		return errors.New("expected agent, pair, revoke, or status")
	}

	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	home := flags.String("home", defaultHome(), "Hopty state directory")
	cancel := flags.Bool("cancel", false, "cancel the pending pairing request")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}

	switch command {
	case "agent":
		if *cancel {
			return errors.New("--cancel is only valid with pair")
		}
		return runAgent(*home)
	case "pair":
		if *cancel {
			return call(*home, "cancel")
		}
		return call(*home, "pair")
	case "revoke":
		return call(*home, "revoke")
	case "status":
		return call(*home, "status")
	default:
		return errors.New("expected agent, pair, revoke, or status")
	}
}

func runAgent(home string) error {
	daemon, err := agent.Start(home)
	if err != nil {
		return err
	}
	defer daemon.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return daemon.Run(ctx)
}

func call(home, command string) error {
	deadline := time.Now().Add(35 * time.Second)
	for {
		response, err := localapi.Call(filepath.Join(home, "run", "agent.sock"), localapi.Request{Command: command})
		if err != nil {
			return fmt.Errorf("agent is not running: %w", err)
		}
		if response.Error == "agent control connection is unavailable" && command == "pair" && time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if response.Error != "" {
			return errors.New(response.Error)
		}
		if response.Status != nil {
			fmt.Printf("connected=%t paired=%t active_terminals=%d\n", response.Status.Connected, response.Status.Paired, response.Status.ActiveTerminals)
		}
		if response.Pairing != nil {
			fmt.Printf("%s\nVerification code: %s\nExpires: %s\n", response.Pairing.URL, response.Pairing.Code, response.Pairing.ExpiresAt)
		}
		return nil
	}
}

func defaultHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".hopty"
	}
	return filepath.Join(home, ".hopty")
}
