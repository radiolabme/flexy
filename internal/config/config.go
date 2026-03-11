// Package config handles XDG-compliant configuration for flexy.
//
// Precedence: CLI flags > config file > defaults.
// Config file location: $XDG_CONFIG_HOME/flexy/config.json
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

const (
	appName    = "flexy"
	configFile = "config.json"

	// CurrentVersion is bumped whenever new config fields are added.
	// The setup wizard stamps this value; on load, a mismatch means
	// the user should re-run --setup.
	CurrentVersion = 2
)

// Config holds all flexy configuration. JSON tags match the CLI flag names.
type Config struct {
	Version     int    `json:"version,omitempty"`
	Radio       string `json:"radio,omitempty"`
	UDPPort     int    `json:"udp_port,omitempty"`
	Station     string `json:"station,omitempty"`
	Slice       string `json:"slice,omitempty"`
	Headless    bool   `json:"headless,omitempty"`
	Listen      string `json:"listen,omitempty"`
	Web         string `json:"web,omitempty"`
	Proxy       string `json:"proxy,omitempty"`
	ProxyIP     string `json:"proxy_ip,omitempty"`
	RadioBindIP string `json:"radio_bind_ip,omitempty"`
	Profile     string `json:"profile,omitempty"`
	LogLevel    string `json:"log_level,omitempty"`
	ChkVFOMode  string `json:"chkvfo_mode,omitempty"`
	Metering    *bool  `json:"metering,omitempty"`
	LogPings    bool   `json:"log_pings,omitempty"`
	ProxyOnly   bool   `json:"proxy_only,omitempty"`
}

// IsStale returns true when the config was saved by an older version
// of the setup wizard and may be missing fields.
func (c *Config) IsStale() bool {
	return c.Version < CurrentVersion
}

// Defaults returns a Config with all default values.
func Defaults() Config {
	m := true
	return Config{
		Radio:      ":discover:",
		Station:    "Flex",
		Slice:      "A",
		Listen:     ":4532",
		LogLevel:   "info",
		ChkVFOMode: "new",
		Metering:   &m,
	}
}

// Path returns the full path to the config file.
func Path() string {
	return filepath.Join(xdg.ConfigHome, appName, configFile)
}

// Load reads the config file. Returns Defaults() merged with file contents.
// If the file doesn't exist, returns Defaults() with no error.
func Load() (Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Defaults(), err
	}
	return cfg, nil
}

// Save writes the config to the XDG config directory.
func Save(cfg *Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o600)
}

// MeteringEnabled returns the effective metering value (default true).
func (c *Config) MeteringEnabled() bool {
	if c.Metering == nil {
		return true
	}
	return *c.Metering
}

// SetMetering is a convenience for setting the metering pointer.
func (c *Config) SetMetering(v bool) {
	c.Metering = &v
}
