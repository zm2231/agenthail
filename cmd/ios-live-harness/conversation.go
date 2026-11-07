package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type conversation struct {
	id, cwd, transcript string
}

var repositoryFiles = map[string]string{"build.sh": "#!/bin/sh\nmake\n", "README.md": "# Build\n", ".gitignore": "*.log\n", "build.log": "ignored\n"}

func (h *host) seedRepository(dir string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return err
	}
	cmd := exec.Command(git, "init", "-q", dir)
	cmd.Env = []string{"HOME=" + h.home, "GIT_CONFIG_NOSYSTEM=1", "PATH=" + os.Getenv("PATH")}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init %s: %v: %s", dir, err, out)
	}
	for name, content := range repositoryFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func (h *host) appendReply(index int, text string) (string, error) {
	if index < 0 || index >= len(h.conversations) {
		return "", fmt.Errorf("no conversation %d", index)
	}
	c := h.conversations[index]
	record, err := json.Marshal(map[string]any{
		"type": "assistant", "uuid": uuid.NewString(), "sessionId": c.id, "cwd": c.cwd, "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"id": "msg-" + uuid.NewString(), "role": "assistant", "model": "claude-sonnet-5-5", "content": []map[string]any{{"type": "text", "text": text}}},
	})
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(c.transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if _, err := file.Write(append(record, '\n')); err != nil {
		return "", err
	}
	return c.id, nil
}
