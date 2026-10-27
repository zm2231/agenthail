package surface

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	ClaudeLoginClaudeAI  = "claude.ai"
	ClaudeLoginAPIKey    = "api_key"
	ClaudeLoginSignedOut = "signed_out"
)

var claudeAPIKeyEnv = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

// Only the presence of key names is read; no secret value leaves the file.
func ClaudeRootLogin(home string, root ClaudeConfigRoot) string {
	if root.SignedInAs != nil && root.SignedInAs.AccountID != "" {
		return ClaudeLoginClaudeAI
	}
	for _, name := range claudeAPIKeyEnv {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return ClaudeLoginAPIKey
		}
	}
	var settings struct {
		APIKeyHelper string            `json:"apiKeyHelper"`
		Env          map[string]string `json:"env"`
	}
	if readClaudeJSON(filepath.Join(root.Root, "settings.json"), &settings) {
		if strings.TrimSpace(settings.APIKeyHelper) != "" {
			return ClaudeLoginAPIKey
		}
		for _, name := range claudeAPIKeyEnv {
			if strings.TrimSpace(settings.Env[name]) != "" {
				return ClaudeLoginAPIKey
			}
		}
	}
	var global struct {
		PrimaryAPIKey string `json:"primaryApiKey"`
	}
	if readClaudeJSON(ClaudeGlobalConfigPath(home, root.Root), &global) && global.PrimaryAPIKey != "" {
		return ClaudeLoginAPIKey
	}
	return ClaudeLoginSignedOut
}

func readClaudeJSON(path string, value any) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxClaudeGlobalConfigBytes+1))
	if err != nil || len(data) > maxClaudeGlobalConfigBytes {
		return false
	}
	return json.Unmarshal(data, value) == nil
}
