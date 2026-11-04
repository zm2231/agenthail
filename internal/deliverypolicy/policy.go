package deliverypolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Mode string

const (
	Queue Mode = "queue"
	Steer Mode = "steer"
)

func Normalize(value string) (Mode, error) {
	if value == "" {
		return Queue, nil
	}
	mode := Mode(value)
	if mode != Queue && mode != Steer {
		return "", fmt.Errorf("busyDelivery must be %q or %q", Queue, Steer)
	}
	return mode, nil
}

// Load always returns a usable mode: one unreadable setting must not stop delivery, so a problem means Queue and is returned to be reported.
func Load() (Mode, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Queue, err
	}
	return LoadFromPath(filepath.Join(home, ".agenthail", "dashboard.json"))
}

func Current() Mode {
	mode, _ := Load()
	return mode
}

func LoadFromPath(path string) (Mode, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Queue, nil
	}
	if err != nil {
		return Queue, fmt.Errorf("read dashboard config: %w", err)
	}
	var config struct {
		BusyDelivery string `json:"busyDelivery"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return Queue, fmt.Errorf("parse dashboard config: %w", err)
	}
	mode, err := Normalize(config.BusyDelivery)
	if err != nil {
		return Queue, err
	}
	return mode, nil
}
