package surface

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type ManagedCodexLaunchReceipt struct {
	LaunchID    string `json:"launchId"`
	ThreadID    string `json:"threadId"`
	Cwd         string `json:"cwd"`
	TmuxSession string `json:"tmuxSession"`
	TmuxPane    string `json:"tmuxPane"`
}

const managedCodexLaunchReceiptMaxBytes = 16 << 10

func ManagedCodexLaunchReceiptPath(launchID string) (string, error) {
	if launchID == "" || filepath.Base(launchID) != launchID || filepath.IsAbs(launchID) {
		return "", errors.New("invalid managed Codex launch ID")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home for managed Codex launch receipt: %w", err)
	}
	return filepath.Join(home, ".agenthail", "launches", launchID+".json"), nil
}

func WriteManagedCodexLaunchReceipt(path string, receipt ManagedCodexLaunchReceipt) error {
	if receipt.LaunchID == "" || receipt.ThreadID == "" || receipt.Cwd == "" || receipt.TmuxSession == "" || receipt.TmuxPane == "" {
		return errors.New("managed Codex launch receipt is incomplete")
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("encode managed Codex launch receipt: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create managed Codex launch receipt directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
	if err != nil {
		return fmt.Errorf("create managed Codex launch receipt: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect managed Codex launch receipt: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write managed Codex launch receipt: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close managed Codex launch receipt: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish managed Codex launch receipt: %w", err)
	}
	return nil
}

func ReadManagedCodexLaunchReceipt(path string) (ManagedCodexLaunchReceipt, error) {
	info, err := os.Stat(path)
	if err != nil {
		return ManagedCodexLaunchReceipt{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > managedCodexLaunchReceiptMaxBytes {
		return ManagedCodexLaunchReceipt{}, errors.New("managed Codex launch receipt is not a bounded private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return ManagedCodexLaunchReceipt{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, managedCodexLaunchReceiptMaxBytes+1))
	if err != nil {
		return ManagedCodexLaunchReceipt{}, fmt.Errorf("read managed Codex launch receipt: %w", err)
	}
	if len(data) > managedCodexLaunchReceiptMaxBytes {
		return ManagedCodexLaunchReceipt{}, errors.New("managed Codex launch receipt exceeds size limit")
	}
	var receipt ManagedCodexLaunchReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return ManagedCodexLaunchReceipt{}, fmt.Errorf("parse managed Codex launch receipt: %w", err)
	}
	if receipt.LaunchID == "" || receipt.ThreadID == "" || receipt.Cwd == "" || receipt.TmuxSession == "" || receipt.TmuxPane == "" {
		return ManagedCodexLaunchReceipt{}, errors.New("managed Codex launch receipt is incomplete")
	}
	return receipt, nil
}
