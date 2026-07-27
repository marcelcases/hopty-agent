// Package config loads the agent's minimal local configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const FileName = "config.toml"

type Config struct{ ServiceURL *url.URL }

func Load(home string) (Config, error) {
	data, err := os.ReadFile(filepath.Join(home, FileName))
	if err != nil {
		return Config{}, err
	}
	line := strings.TrimSpace(string(data))
	key, value, ok := strings.Cut(line, "=")
	if !ok || strings.TrimSpace(key) != "service_url" {
		return Config{}, errors.New("config.toml must contain only service_url")
	}
	value = strings.TrimSpace(value)
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return Config{}, errors.New("service_url must be a quoted URL")
	}
	value = value[1 : len(value)-1]
	if value == "" || strings.Contains(value, `"`) {
		return Config{}, errors.New("service_url must be a quoted URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Config{}, fmt.Errorf("service_url must be an HTTPS origin")
	}
	return Config{ServiceURL: parsed}, nil
}
