package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
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
		return errors.New("expected agent, pair, revoke, status, or uninstall")
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
		return revoke(*home)
	case "uninstall":
		if *wait {
			return errors.New("--wait is only valid with pair")
		}
		return uninstall(*home)
	case "status":
		if *wait {
			return errors.New("--wait is only valid with pair")
		}
		return call(*home, "status")
	default:
		return errors.New("expected agent, pair, revoke, status, or uninstall")
	}
}

func runAgent(home string) error {
	daemon, err := agent.Start(home, version)
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

func revoke(home string) error {
	response, err := request(home, "revoke", true)
	if err != nil {
		return err
	}
	_ = response
	fmt.Println("Passkey revoked.")
	return nil
}

func printStatus(status localapi.Status) {
	versionText := strings.TrimPrefix(status.AgentVersion, "v")
	if versionText == "" {
		versionText = "unknown"
	}
	if !status.Paired {
		fmt.Printf("Hopty Agent v%s is installed.\n\nTo create a passkey, run hopty pair\nTo uninstall, run hopty uninstall\n", versionText)
		return
	}
	fmt.Printf("Hopty Agent v%s is up.\n\n", versionText)
	if len(status.Sessions) == 0 {
		fmt.Print("No active sessions.\n\nGo to hopty.net to access your terminal\n\n")
		printStatusTimes(status)
		fmt.Print("\nTo revoke passkey, run hopty revoke\nTo uninstall, run hopty uninstall\n")
		return
	}
	fmt.Printf("Active sessions     %d\n\n", len(status.Sessions))
	for index, session := range status.Sessions {
		if index > 0 {
			fmt.Println()
		}
		fmt.Printf("User                %s\nConnection          %s\nTransport           %s\nLatency             %d ms\nIncoming IP         %s\n", valueOr(session.User, "unknown"), valueOr(session.Connection, "unknown"), valueOr(session.Transport, "unknown"), session.LatencyMS, valueOr(session.IncomingIP, "unknown"))
	}
	printStatusTimes(status)
	fmt.Print("\nTo revoke passkey, run hopty revoke\nTo uninstall, run hopty uninstall\n")
}

func printStatusTimes(status localapi.Status) {
	fmt.Printf("Last access         %s\nPasskey created     %s\n", formatTime(status.LastAccessAt), formatTime(status.PasskeyCreatedAt))
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func formatTime(value *time.Time) string {
	if value == nil {
		return "Never"
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}

const managedShellBlock = "# Hopty\n. \"$HOME/.local/bin/env\"\nexport PATH=\"$HOME/.local/bin:$PATH\"\n# End Hopty\n"
const managedEnvFile = "# Hopty-managed local environment.\nexport PATH=\"$HOME/.local/bin:$PATH\"\n"

func uninstall(home string) error {
	if _, err := request(home, "revoke", true); err != nil && !strings.Contains(err.Error(), "agent is not running") {
		return err
	}
	if err := stopUserService(); err != nil {
		return err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	localBin := filepath.Join(userHome, ".local", "bin")
	hoptyLink := filepath.Join(localBin, "hopty")
	if target, readErr := os.Readlink(hoptyLink); readErr == nil {
		resolvedTarget, _ := filepath.EvalSymlinks(filepath.Join(filepath.Dir(hoptyLink), target))
		resolvedHome, _ := filepath.EvalSymlinks(home)
		if resolvedTarget == filepath.Join(resolvedHome, "bin", "hopty") {
			if err := os.Remove(hoptyLink); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	for _, profile := range []string{filepath.Join(userHome, ".bashrc"), filepath.Join(userHome, ".profile")} {
		if err := removeShellBlock(profile); err != nil {
			return err
		}
	}
	envFile := filepath.Join(localBin, "env")
	if data, readErr := os.ReadFile(envFile); readErr == nil && string(data) == managedEnvFile {
		if err := os.Remove(envFile); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(home); err != nil {
		return err
	}
	fmt.Println("Hopty Agent uninstalled successfully.")
	return nil
}

func stopUserService() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil
	}
	status := exec.Command("systemctl", "--user", "is-active", "--quiet", "hopty.service").Run()
	if status == nil {
		if err := exec.Command("systemctl", "--user", "disable", "--now", "hopty.service").Run(); err != nil {
			return fmt.Errorf("could not stop hopty.service: %w", err)
		}
	} else {
		_ = exec.Command("systemctl", "--user", "disable", "hopty.service").Run()
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func removeShellBlock(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	updated := strings.ReplaceAll(string(data), managedShellBlock, "")
	if updated == string(data) {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(updated), info.Mode().Perm())
}

func defaultHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".hopty"
	}
	return filepath.Join(home, ".hopty")
}
