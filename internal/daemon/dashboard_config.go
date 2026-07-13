package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

const (
	defaultDashboardListen  = "127.0.0.1:7412"
	defaultCodexRecentHours = 5
	maximumCodexRecentHours = 168
)

type DashboardConfig struct {
	Enabled          bool               `json:"enabled"`
	Listen           string             `json:"listen"`
	CodexRecentHours int                `json:"codexRecentHours"`
	RemoteAccess     RemoteAccessConfig `json:"remoteAccess"`
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
		return DashboardConfig{Listen: defaultDashboardListen, CodexRecentHours: defaultCodexRecentHours, RemoteAccess: normalizeRemoteAccessConfig(RemoteAccessConfig{})}, nil
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

func DashboardURL() (string, error) {
	config, err := LoadDashboardConfig()
	if err != nil {
		return "", err
	}
	if !config.Enabled {
		return "", fmt.Errorf("dashboard is disabled; run 'agenthail dashboard enable'")
	}
	token, err := dashboardToken()
	if err != nil {
		return "", err
	}
	return "http://" + config.Listen + "/?token=" + token, nil
}

func dashboardToken() (string, error) {
	data, err := os.ReadFile(DashboardTokenPath())
	if err == nil && len(data) >= 32 {
		return string(data), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read dashboard token: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate dashboard token: %w", err)
	}
	token := hex.EncodeToString(raw)
	if err := os.WriteFile(DashboardTokenPath(), []byte(token), 0600); err != nil {
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
