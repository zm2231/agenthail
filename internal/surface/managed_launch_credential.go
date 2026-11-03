package surface

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/atomicfile"
)

type LaunchMCPCredential struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Ref   string `json:"ref"`
}

const managedCodexLaunchCredentialMaxBytes = 4 << 10

func ManagedCodexLaunchCredentialPath(launchID string) (string, error) {
	receipt, err := ManagedCodexLaunchReceiptPath(launchID)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(receipt, ".json") + ".mcp", nil
}

func writeManagedCodexLaunchCredential(launchID string, credential *LaunchMCPCredential) (string, error) {
	if credential == nil {
		return "", nil
	}
	if credential.URL == "" || credential.Token == "" || credential.Ref == "" {
		return "", errors.New("managed Codex launch credential is incomplete")
	}
	path, err := ManagedCodexLaunchCredentialPath(launchID)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(credential)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", fmt.Errorf("create managed Codex launch directory: %w", err)
	}
	if err := atomicfile.Write(path, data, 0600); err != nil {
		return "", fmt.Errorf("write managed Codex launch credential: %w", err)
	}
	return path, nil
}

func TakeManagedCodexLaunchCredential(launchID string) (*LaunchMCPCredential, error) {
	path, err := ManagedCodexLaunchCredentialPath(launchID)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > managedCodexLaunchCredentialMaxBytes {
		return nil, errors.New("managed Codex launch credential is not a bounded private regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, managedCodexLaunchCredentialMaxBytes+1))
	if err != nil {
		return nil, err
	}
	var credential LaunchMCPCredential
	if err := json.Unmarshal(data, &credential); err != nil {
		return nil, fmt.Errorf("parse managed Codex launch credential: %w", err)
	}
	if credential.URL == "" || credential.Token == "" || credential.Ref == "" {
		return nil, errors.New("managed Codex launch credential is incomplete")
	}
	return &credential, nil
}
