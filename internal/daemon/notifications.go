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

	"github.com/zm2231/agenthail/internal/surface"
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

// Notify shows a desktop notification. A non-empty identifier replaces an
// earlier notification with the same identifier rather than adding one.
func Notify(title, message, sessionID, identifier string) error {
	return NotifyRequest(title, message, sessionID, identifier, "", nil)
}

// NotifyRequest is Notify for a session's open request, with the buttons that
// answer it from the notification when it has any.
func NotifyRequest(title, message, sessionID, identifier, requestID string, actions []surface.RequestAction) error {
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
	if identifier == "" {
		identifier = fmt.Sprintf("agenthail-%d", time.Now().UnixNano())
	}
	args := []string{
		"send",
		"--title", boundedNotificationText(title, 120),
		"--message", boundedNotificationText(message, 1200),
		"--identifier", identifier,
	}
	if sessionID != "" {
		args = append(args, "--session", sessionID)
	}
	if requestID != "" {
		args = append(args, "--request", requestID)
	}
	if requestID != "" && len(actions) > 0 {
		encoded, err := json.Marshal(actions)
		if err != nil {
			return err
		}
		args = append(args, "--actions", string(encoded))
	}
	_, err = runNotificationHelper(5*time.Second, args...)
	return err
}

// WithdrawNotification removes a delivered desktop notification, such as one
// for a question that was answered elsewhere.
func WithdrawNotification(identifier string) error {
	if runtime.GOOS != "darwin" || identifier == "" {
		return nil
	}
	config, err := LoadNotificationConfig()
	if err != nil || !config.Enabled {
		return err
	}
	_, err = runNotificationHelper(5*time.Second, "withdraw", "--identifier", identifier)
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

// installedMacApp is where the macOS package installs Agenthail.app.
const installedMacApp = "/Applications/Agenthail.app/Contents/MacOS/Agenthail"

// notificationHelperPath is the installed Agenthail app that posts and
// withdraws desktop notifications: the one the installer names in
// AGENTHAIL_MAC_APP, or the package's copy in /Applications. Other copies,
// such as an old one left in ~/Applications, are never used.
func notificationHelperPath() (string, error) {
	path := strings.TrimSpace(os.Getenv("AGENTHAIL_MAC_APP"))
	if path == "" {
		path = installedMacApp
	}
	if !isExecutableFile(path) {
		return "", fmt.Errorf("the Agenthail macOS app is not installed at %s; reinstall Agenthail to enable notifications", path)
	}
	return path, nil
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
