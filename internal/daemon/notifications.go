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
	if err := os.MkdirAll(filepath.Dir(NotificationConfigPath()), 0700); err != nil {
		return fmt.Errorf("create notification config directory: %w", err)
	}
	return os.WriteFile(NotificationConfigPath(), append(data, '\n'), 0600)
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
	script := fmt.Sprintf("display notification %s with title %s", appleScriptString(message), appleScriptString(title))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("send desktop notification: %w", ctx.Err())
		}
		return fmt.Errorf("send desktop notification: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func appleScriptString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return `"` + value + `"`
}
