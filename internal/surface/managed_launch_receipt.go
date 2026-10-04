package surface

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ManagedCodexLaunchReceipt struct {
	LaunchID    string `json:"launchId"`
	ThreadID    string `json:"threadId"`
	Cwd         string `json:"cwd"`
	Runtime     string `json:"runtime"`
	TmuxSession string `json:"tmuxSession"`
	TmuxPane    string `json:"tmuxPane"`
	Workspace   string `json:"workspace,omitempty"`
	Surface     string `json:"surface,omitempty"`
}

const managedCodexLaunchReceiptMaxBytes = 16 << 10
const managedCodexLaunchReceiptReclaimLimit = 128

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

func managedCodexLaunchReceiptDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home for managed Codex launch receipts: %w", err)
	}
	return filepath.Join(home, ".agenthail", "launches"), nil
}

func ReclaimManagedCodexLaunchReceipts(liveSessions map[string]struct{}, verifyCurrent func(string) (bool, error)) error {
	if verifyCurrent == nil {
		return errors.New("managed Codex receipt reclaim requires current-session verification")
	}
	return reclaimManagedCodexLaunchReceipts(".reclaim-cursor", func(receipt ManagedCodexLaunchReceipt) (bool, error) {
		if receipt.Runtime != LauncherTMUX || receipt.TmuxSession == "" || receipt.TmuxPane == "" {
			return true, nil
		}
		if _, live := liveSessions[receipt.TmuxSession]; live {
			return true, nil
		}
		return verifyCurrent(receipt.TmuxSession)
	})
}

func ReclaimManagedCodexCMUXLaunchReceipts(verifyCurrent func(string, string) (bool, error)) error {
	if verifyCurrent == nil {
		return errors.New("managed Codex CMUX receipt reclaim requires current-surface verification")
	}
	return reclaimManagedCodexLaunchReceipts(".cmux-reclaim-cursor", func(receipt ManagedCodexLaunchReceipt) (bool, error) {
		if receipt.Runtime != LauncherCMUX || receipt.Workspace == "" || receipt.Surface == "" {
			return true, nil
		}
		return verifyCurrent(receipt.Workspace, receipt.Surface)
	})
}

func reclaimManagedCodexLaunchReceipts(cursorName string, verifyCurrent func(ManagedCodexLaunchReceipt) (bool, error)) error {
	dir, err := managedCodexLaunchReceiptDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	candidates := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "agenthail-") && strings.HasSuffix(entry.Name(), ".json") {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	cursorPath := filepath.Join(dir, cursorName)
	cursorBytes, _ := os.ReadFile(cursorPath)
	cursor := strings.TrimSpace(string(cursorBytes))
	start := 0
	if cursor != "" {
		for index, entry := range candidates {
			if entry.Name() > cursor {
				start = index
				break
			}
			start = (index + 1) % len(candidates)
		}
	}
	lastScanned := ""
	for scanned := 0; scanned < len(candidates) && scanned < managedCodexLaunchReceiptReclaimLimit; scanned++ {
		entry := candidates[(start+scanned)%len(candidates)]
		lastScanned = entry.Name()
		launchID := strings.TrimSuffix(entry.Name(), ".json")
		path, err := ManagedCodexLaunchReceiptPath(launchID)
		if err != nil || filepath.Clean(path) != filepath.Clean(filepath.Join(dir, entry.Name())) {
			continue
		}
		receipt, err := ReadManagedCodexLaunchReceipt(path)
		if err != nil {
			continue
		}
		if receipt.LaunchID != launchID {
			continue
		}
		current, err := verifyCurrent(receipt)
		if err != nil || current {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if lastScanned != "" {
		if err := os.WriteFile(cursorPath, []byte(lastScanned+"\n"), 0600); err != nil {
			return err
		}
	}
	return nil
}

func WriteManagedCodexLaunchReceipt(path string, receipt ManagedCodexLaunchReceipt) error {
	if !managedCodexLaunchReceiptComplete(receipt) {
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
	if !managedCodexLaunchReceiptComplete(receipt) {
		return ManagedCodexLaunchReceipt{}, errors.New("managed Codex launch receipt is incomplete")
	}
	return receipt, nil
}

func managedCodexLaunchReceiptComplete(receipt ManagedCodexLaunchReceipt) bool {
	if receipt.LaunchID == "" || receipt.ThreadID == "" || receipt.Cwd == "" {
		return false
	}
	if receipt.Runtime == LauncherTMUX {
		return receipt.TmuxSession != "" && receipt.TmuxPane != "" && receipt.Workspace == "" && receipt.Surface == ""
	}
	if receipt.Runtime == LauncherCMUX {
		return receipt.Workspace != "" && receipt.Surface != "" && receipt.TmuxSession == "" && receipt.TmuxPane == ""
	}
	return false
}
