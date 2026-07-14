package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type NotificationConfig struct {
	Enabled bool `json:"enabled"`
}

type NotificationStatus struct {
	Enabled       bool   `json:"enabled"`
	Available     bool   `json:"available"`
	Authorization string `json:"authorization"`
	Authorized    bool   `json:"authorized"`
	Alerts        bool   `json:"alerts"`
	Sounds        bool   `json:"sounds"`
	Error         string `json:"error,omitempty"`
}

func NotificationConfigPath() string {
	return filepath.Join(filepath.Dir(PidFilePath()), "notifications.json")
}

func LoadNotificationConfig() (NotificationConfig, error) {
	data, err := os.ReadFile(NotificationConfigPath())
	if os.IsNotExist(err) {
		return NotificationConfig{}, nil
	}
	if err != nil {
		return NotificationConfig{}, fmt.Errorf("read notification config: %w", err)
	}
	var config NotificationConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return NotificationConfig{}, fmt.Errorf("parse notification config: %w", err)
	}
	return config, nil
}

func SaveNotificationConfig(config NotificationConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Dir(NotificationConfigPath())
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create notification config directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".notifications-*.json")
	if err != nil {
		return fmt.Errorf("create notification config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, NotificationConfigPath())
}

func GetNotificationStatus() NotificationStatus {
	config, err := LoadNotificationConfig()
	if err != nil {
		return NotificationStatus{Error: err.Error()}
	}
	status := nativeNotificationStatus("status", 5*time.Second)
	status.Enabled = config.Enabled && status.Authorized
	return status
}

func EnableNotifications() (NotificationStatus, error) {
	status := nativeNotificationStatus("request", 125*time.Second)
	if status.Error != "" {
		return status, fmt.Errorf("enable desktop notifications: %s", status.Error)
	}
	if !status.Authorized {
		status.Enabled = false
		if err := SaveNotificationConfig(NotificationConfig{}); err != nil {
			return status, err
		}
		return status, fmt.Errorf("desktop notifications are %s; allow Agenthail in System Settings", status.Authorization)
	}
	status.Enabled = true
	if err := SaveNotificationConfig(NotificationConfig{Enabled: true}); err != nil {
		return status, err
	}
	return status, nil
}

func DisableNotifications() error {
	return SaveNotificationConfig(NotificationConfig{})
}

func OpenNotificationSettings() error {
	_, err := runNotificationHelper(5*time.Second, "settings")
	return err
}

func Notify(title, message string) error {
	config, err := LoadNotificationConfig()
	if err != nil {
		return err
	}
	if !config.Enabled {
		return nil
	}
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("desktop notifications currently support macOS only")
	}
	_, err = runNotificationHelper(5*time.Second,
		"send",
		"--title", boundedNotificationText(title, 120),
		"--message", boundedNotificationText(message, 1200),
		"--identifier", fmt.Sprintf("agenthail-%d", time.Now().UnixNano()),
	)
	return err
}

func nativeNotificationStatus(command string, timeout time.Duration) NotificationStatus {
	if runtime.GOOS != "darwin" {
		return NotificationStatus{Authorization: "unsupported", Error: "desktop notifications currently support macOS only"}
	}
	output, err := runNotificationHelper(timeout, command)
	if err != nil {
		return NotificationStatus{Authorization: "unavailable", Error: err.Error()}
	}
	var status NotificationStatus
	if err := json.Unmarshal(output, &status); err != nil {
		return NotificationStatus{Authorization: "unavailable", Error: fmt.Sprintf("parse native notification status: %s", err)}
	}
	return status
}

func runNotificationHelper(timeout time.Duration, args ...string) ([]byte, error) {
	path, err := notificationHelperPath()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("native notification helper: %w", ctx.Err())
		}
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("native notification helper: %s", message)
	}
	return output, nil
}

func notificationHelperPath() (string, error) {
	if value := strings.TrimSpace(os.Getenv("AGENTHAIL_MAC_APP")); value != "" {
		if isExecutableFile(value) {
			return value, nil
		}
		return "", fmt.Errorf("Agenthail macOS app helper is unavailable at %s", value)
	}
	home, _ := os.UserHomeDir()
	executable, _ := os.Executable()
	candidates := []string{
		filepath.Join(filepath.Dir(executable), "Agenthail.app", "Contents", "MacOS", "Agenthail"),
		filepath.Join(home, ".local", "share", "agenthail", "Agenthail.app", "Contents", "MacOS", "Agenthail"),
		filepath.Join(home, "Applications", "Agenthail.app", "Contents", "MacOS", "Agenthail"),
		filepath.Join("/Applications", "Agenthail.app", "Contents", "MacOS", "Agenthail"),
	}
	for _, candidate := range candidates {
		if isExecutableFile(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("Agenthail macOS app is not installed; reinstall Agenthail to enable notifications")
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0111 != 0
}

func boundedNotificationText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
