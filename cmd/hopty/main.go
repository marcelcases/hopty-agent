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
	wait := flags.Bool("wait", false, "wait for browser pairing to complete")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *cancel && command != "pair" {
		return errors.New("--cancel is only valid with pair")
	}

	switch command {
	case "agent":
		if *cancel {
			return errors.New("--cancel is only valid with pair")
		}
		return runAgent(*home)
	case "pair":
		if *cancel && *wait {
			return errors.New("--cancel and --wait cannot be used together")
		}
		if *cancel {
			return call(*home, "cancel")
		}
		return pair(*home, *wait)
	case "revoke":
		if *wait {
			return errors.New("--wait is only valid with pair")
		}
		return call(*home, "revoke")
	case "status":
		if *wait {
			return errors.New("--wait is only valid with pair")
		}
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
	response, err := request(home, command, command == "pair")
	if err != nil {
		return err
	}
	if response.Status != nil {
		printStatus(*response.Status)
	}
	return nil
}

func pair(home string, wait bool) error {
	response, err := request(home, "pair", true)
	if err != nil {
		return err
	}
	if response.Pairing == nil {
		return errors.New("pairing request unavailable")
	}
	fmt.Printf("\nLink this host\nOpen the private URL below, enter its verification code, then create your passkey.\n  %s\n\nVerification code: %s\n", response.Pairing.URL, response.Pairing.Code)
	if !wait {
		return nil
	}
	expiresAt, err := time.Parse(time.RFC3339, response.Pairing.ExpiresAt)
	if err != nil {
		return err
	}
	for time.Now().Before(expiresAt) {
		response, err := request(home, "status", false)
		if err == nil && response.Status != nil && response.Status.PairingVerified {
			fmt.Print("\r\033[K✓ Verification code confirmed. Finish passkey setup in your browser.\n")
			return nil
		}
		remaining := time.Until(expiresAt).Round(time.Second)
		fmt.Printf("\r\033[KWaiting for browser pairing · expires in %02d:%02d", int(remaining.Minutes()), int(remaining.Seconds())%60)
		time.Sleep(time.Second)
	}
	fmt.Print("\r\033[KPairing link expired. Run hopty pair to try again.\n")
	return nil
}

func request(home, command string, retryUnavailable bool) (localapi.Response, error) {
	deadline := time.Now().Add(35 * time.Second)
	for {
		response, err := localapi.Call(filepath.Join(home, "run", "agent.sock"), localapi.Request{Command: command})
		if err != nil {
			return localapi.Response{}, fmt.Errorf("agent is not running: %w", err)
		}
		if response.Error == "agent control connection is unavailable" && retryUnavailable && time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if response.Error != "" {
			return localapi.Response{}, errors.New(response.Error)
		}
		return response, nil
	}
}

func printStatus(status localapi.Status) {
	connection := "offline"
	if status.Connected {
		connection = "connected"
	}
	pairing := "awaiting pairing"
	if status.PairingVerified {
		pairing = "code verified"
	}
	if status.Paired {
		pairing = "paired"
	}
	fmt.Printf("Hopty status\n  Connection  %s\n  Host        %s\n  Terminals   %d active\n", connection, pairing, status.ActiveTerminals)
}

func defaultHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".hopty"
	}
	return filepath.Join(home, ".hopty")
}
