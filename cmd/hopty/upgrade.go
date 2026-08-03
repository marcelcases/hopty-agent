package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/marcelcases/hopty-agent/internal/config"
)

const (
	upgradeEndpointPath   = "/api/v1/agent/upgrade"
	maxUpgradeScriptBytes = 1 << 20
	upgradeRequestTimeout = 35 * time.Second
)

type agentRelease struct {
	Version     string
	SHA256AMD64 string
	SHA256ARM64 string
}

func upgrade(home string) error {
	return upgradeWithClient(home, upgradeHTTPClient())
}

func upgradeWithClient(home string, client *http.Client) error {
	if runtime.GOOS != "linux" {
		return errors.New("Hopty upgrades are supported on Linux only")
	}
	loaded, err := config.Load(home)
	if err != nil {
		return fmt.Errorf("could not load agent configuration: %w", err)
	}
	script, release, err := fetchLatestInstaller(client, loaded.ServiceURL)
	if err != nil {
		return err
	}
	if versionsMatch(version, release.Version) {
		fmt.Printf("Hopty Agent %s is already up to date.\n", displayVersion(release.Version))
		return nil
	}
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Join(home, "tmp"), "hopty-upgrade-*.sh")
	if err != nil {
		return err
	}
	scriptPath := file.Name()
	defer os.Remove(scriptPath)
	if err := file.Chmod(0o700); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(script); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	fmt.Printf("Upgrading Hopty Agent from %s to %s...\n", displayVersion(version), displayVersion(release.Version))
	command := exec.Command("/bin/sh", scriptPath)
	command.Env = upgradeEnvironment(home)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("agent upgrade failed: %w", err)
	}
	return nil
}

func upgradeHTTPClient() *http.Client {
	return &http.Client{
		Timeout: upgradeRequestTimeout,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" {
				return errors.New("agent upgrade redirect was not HTTPS")
			}
			return nil
		},
	}
}

func fetchLatestInstaller(client *http.Client, serviceURL *url.URL) ([]byte, agentRelease, error) {
	endpoint := *serviceURL
	endpoint.Path = upgradeEndpointPath
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	request, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, agentRelease{}, fmt.Errorf("could not create upgrade request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, agentRelease{}, fmt.Errorf("could not fetch the latest agent installer: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, agentRelease{}, fmt.Errorf("latest agent installer request failed: HTTP %d", response.StatusCode)
	}
	script, err := io.ReadAll(io.LimitReader(response.Body, maxUpgradeScriptBytes+1))
	if err != nil {
		return nil, agentRelease{}, fmt.Errorf("could not read the latest agent installer: %w", err)
	}
	if len(script) > maxUpgradeScriptBytes {
		return nil, agentRelease{}, errors.New("latest agent installer is too large")
	}
	release, err := parseInstallerRelease(script)
	if err != nil {
		return nil, agentRelease{}, err
	}
	return script, release, nil
}

func parseInstallerRelease(script []byte) (agentRelease, error) {
	if !strings.HasPrefix(string(script), "#!/bin/sh\n") {
		return agentRelease{}, errors.New("latest agent installer is invalid")
	}
	versionValue, err := installerValue(script, "HOPTY_VERSION")
	if err != nil || !validAgentVersion(versionValue) {
		return agentRelease{}, errors.New("latest agent installer has an invalid version")
	}
	amd64, err := installerValue(script, "HOPTY_SHA256_AMD64")
	if err != nil || !validChecksum(amd64) {
		return agentRelease{}, errors.New("latest agent installer has an invalid amd64 checksum")
	}
	arm64, err := installerValue(script, "HOPTY_SHA256_ARM64")
	if err != nil || !validChecksum(arm64) {
		return agentRelease{}, errors.New("latest agent installer has an invalid arm64 checksum")
	}
	return agentRelease{Version: versionValue, SHA256AMD64: amd64, SHA256ARM64: arm64}, nil
}

func installerValue(script []byte, name string) (string, error) {
	prefix := name + "='"
	for _, line := range strings.Split(string(script), "\n") {
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, "'") {
			continue
		}
		value := strings.TrimSuffix(strings.TrimPrefix(line, prefix), "'")
		if value == "" || strings.ContainsAny(value, "'\r\n") {
			return "", errors.New("invalid installer value")
		}
		return value, nil
	}
	return "", fmt.Errorf("installer value %s is missing", name)
}

func validAgentVersion(value string) bool {
	if len(value) < 2 || value[0] != 'v' || !isVersionAlphaNumeric(value[1]) {
		return false
	}
	for _, character := range value[1:] {
		if !isVersionAlphaNumeric(byte(character)) && !strings.ContainsRune("._-", character) {
			return false
		}
	}
	return true
}

func isVersionAlphaNumeric(character byte) bool {
	return (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
}

func validChecksum(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func upgradeEnvironment(home string) []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, value := range os.Environ() {
		if strings.HasPrefix(value, "HOPTY_HOME=") || strings.HasPrefix(value, "HOPTY_UPGRADE=") {
			continue
		}
		environment = append(environment, value)
	}
	environment = append(environment, "HOPTY_HOME="+home, "HOPTY_UPGRADE=1")
	return environment
}

func versionsMatch(current, expected string) bool {
	return strings.TrimPrefix(current, "v") == strings.TrimPrefix(expected, "v")
}

func displayVersion(value string) string {
	if value == "" {
		return "unknown"
	}
	if value == "dev" || strings.HasPrefix(value, "v") {
		return value
	}
	return "v" + value
}
