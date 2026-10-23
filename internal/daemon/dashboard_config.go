package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
)

const (
	defaultDashboardListen  = "127.0.0.1:7412"
	defaultCodexRecentHours = 5
	maximumCodexRecentHours = 168
)

type DashboardConfig struct {
	Enabled          bool                `json:"enabled"`
	Listen           string              `json:"listen"`
	CodexRecentHours int                 `json:"codexRecentHours"`
	BusyDelivery     deliverypolicy.Mode `json:"busyDelivery"`
	RemoteAccess     RemoteAccessConfig  `json:"remoteAccess"`
	// ResumeAfterLimit turns off resuming sessions a usage limit stopped
	// when it is false; unset leaves it on.
	ResumeAfterLimit *bool `json:"resumeAfterLimit,omitempty"`
}

// ResumesAfterLimit reports whether sessions a usage limit stopped resume
// when it resets.
func (c DashboardConfig) ResumesAfterLimit() bool {
	return c.ResumeAfterLimit == nil || *c.ResumeAfterLimit
}

func DashboardConfigPath() string {
	return filepath.Join(filepath.Dir(PidFilePath()), "dashboard.json")
}
func DashboardTokenPath() string {
	return filepath.Join(filepath.Dir(PidFilePath()), "dashboard.token")
}

func LoadDashboardConfig() (DashboardConfig, error) {
	data, err := os.ReadFile(DashboardConfigPath())
	if os.IsNotExist(err) {
		return DashboardConfig{Listen: defaultDashboardListen, CodexRecentHours: defaultCodexRecentHours, BusyDelivery: deliverypolicy.Queue, RemoteAccess: normalizeRemoteAccessConfig(RemoteAccessConfig{})}, nil
	}
	if err != nil {
		return DashboardConfig{}, fmt.Errorf("read dashboard config: %w", err)
	}
	var config DashboardConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return DashboardConfig{}, fmt.Errorf("parse dashboard config: %w", err)
	}
	if config.Listen == "" {
		config.Listen = defaultDashboardListen
	}
	if config.CodexRecentHours == 0 {
		config.CodexRecentHours = defaultCodexRecentHours
	}
	config.BusyDelivery, err = deliverypolicy.Normalize(string(config.BusyDelivery))
	if err != nil {
		return DashboardConfig{}, err
	}
	config.RemoteAccess = normalizeRemoteAccessConfig(config.RemoteAccess)
	if err := validateCodexRecentHours(config.CodexRecentHours); err != nil {
		return DashboardConfig{}, err
	}
	if err := validateDashboardListen(config.Listen); err != nil {
		return DashboardConfig{}, err
	}
	return config, nil
}

func SaveDashboardConfig(config DashboardConfig) error {
	if config.Listen == "" {
		config.Listen = defaultDashboardListen
	}
	if config.CodexRecentHours == 0 {
		config.CodexRecentHours = defaultCodexRecentHours
	}
	var err error
	config.BusyDelivery, err = deliverypolicy.Normalize(string(config.BusyDelivery))
	if err != nil {
		return err
	}
	config.RemoteAccess = normalizeRemoteAccessConfig(config.RemoteAccess)
	if err := validateCodexRecentHours(config.CodexRecentHours); err != nil {
		return err
	}
	if err := validateDashboardListen(config.Listen); err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(DashboardConfigPath()), 0700); err != nil {
		return fmt.Errorf("create dashboard config directory: %w", err)
	}
	return os.WriteFile(DashboardConfigPath(), append(data, '\n'), 0600)
}

func validateCodexRecentHours(hours int) error {
	if hours < 1 || hours > maximumCodexRecentHours {
		return fmt.Errorf("Codex recent hours must be between 1 and %d", maximumCodexRecentHours)
	}
	return nil
}

// DashboardBaseURL is the dashboard's address. It carries no token: a browser
// signs in with a login link from 'agenthail dashboard'.
func DashboardBaseURL(config DashboardConfig) string {
	return "http://" + config.Listen + "/"
}

func dashboardToken() (string, error) {
	data, err := os.ReadFile(DashboardTokenPath())
	if err == nil && len(data) >= 32 {
		return string(data), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read dashboard token: %w", err)
	}
	return writeNewDashboardToken()
}

// RotateDashboardToken replaces the token the local CLI and the Mac app use.
// The running daemon reads it at start, so it takes effect on restart.
func RotateDashboardToken() error {
	_, err := writeNewDashboardToken()
	return err
}

func writeNewDashboardToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate dashboard token: %w", err)
	}
	token := hex.EncodeToString(raw)
	path := DashboardTokenPath()
	temporary, err := os.CreateTemp(filepath.Dir(path), ".dashboard.token-*")
	if err != nil {
		return "", fmt.Errorf("write dashboard token: %w", err)
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.WriteString(token); err != nil {
		temporary.Close()
		return "", fmt.Errorf("write dashboard token: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("write dashboard token: %w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return "", fmt.Errorf("write dashboard token: %w", err)
	}
	return token, nil
}

func validateDashboardListen(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return fmt.Errorf("dashboard listen address must be host:port (got %q)", listen)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("dashboard only accepts a loopback listener; use Tailscale Serve for remote access")
	}
	return nil
}
