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
	ClaudeLoginProxy     = "proxy"
)

const (
	ClaudeSourceAPIKey        = "api_key"
	ClaudeSourceOAuthToken    = "oauth_token"
	ClaudeSourceBedrock       = "bedrock"
	ClaudeSourceVertex        = "vertex"
	ClaudeSourceFoundry       = "foundry"
	ClaudeSourceCloudProvider = "cloud_provider"
)

// In the order Claude checks them.
var claudeProviderSwitches = []struct{ name, source string }{
	{"CLAUDE_CODE_USE_BEDROCK", ClaudeSourceBedrock},
	{"CLAUDE_CODE_USE_VERTEX", ClaudeSourceVertex},
	{"CLAUDE_CODE_USE_FOUNDRY", ClaudeSourceFoundry},
	{"CLAUDE_CODE_USE_ANTHROPIC_AWS", ClaudeSourceCloudProvider},
	{"CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD", ClaudeSourceCloudProvider},
	{"CLAUDE_CODE_USE_MANTLE", ClaudeSourceCloudProvider},
}

var claudeSourceNames = map[string]struct{ title, phrase string }{
	ClaudeSourceAPIKey:        {"API key", "an API key"},
	ClaudeSourceOAuthToken:    {"OAuth token", "an OAuth token"},
	ClaudeSourceBedrock:       {"Bedrock", "Amazon Bedrock"},
	ClaudeSourceVertex:        {"Vertex AI", "Google Vertex AI"},
	ClaudeSourceFoundry:       {"Foundry", "Microsoft Foundry"},
	ClaudeSourceCloudProvider: {"cloud provider", "a cloud provider"},
}

func ClaudeLoginTitle(login, source string) string {
	switch login {
	case ClaudeLoginAPIKey:
		if name, ok := claudeSourceNames[source]; ok {
			return name.title
		}
		return claudeSourceNames[ClaudeSourceAPIKey].title
	case ClaudeLoginSignedOut:
		return "signed out"
	}
	return ""
}

func ClaudeLoginPhrase(source string) string {
	if name, ok := claudeSourceNames[source]; ok {
		return name.phrase
	}
	return claudeSourceNames[ClaudeSourceAPIKey].phrase
}

var claudeAPIKeyEnv = []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"}

// ClaudeRootLogin reads the environment the host launches Claude with, its
// own, under the root's settings.json env, and only whether each variable is set.
func ClaudeRootLogin(home string, root ClaudeConfigRoot) (login, source string) {
	if root.Proxy != "" {
		return ClaudeLoginProxy, ""
	}
	var settings struct {
		APIKeyHelper string            `json:"apiKeyHelper"`
		Env          map[string]string `json:"env"`
	}
	readClaudeJSON(filepath.Join(root.Root, "settings.json"), &settings)
	set := func(name string) bool {
		if value, ok := settings.Env[name]; ok {
			return strings.TrimSpace(value) != ""
		}
		return strings.TrimSpace(os.Getenv(name)) != ""
	}
	on := func(name string) bool {
		value, ok := settings.Env[name]
		if !ok {
			value = os.Getenv(name)
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			return true
		}
		return false
	}
	for _, provider := range claudeProviderSwitches {
		if on(provider.name) {
			return ClaudeLoginAPIKey, provider.source
		}
	}
	if root.SignedInAs != nil && root.SignedInAs.AccountID != "" {
		return ClaudeLoginClaudeAI, ""
	}
	for _, name := range claudeAPIKeyEnv {
		if set(name) {
			return ClaudeLoginAPIKey, ClaudeSourceAPIKey
		}
	}
	if strings.TrimSpace(settings.APIKeyHelper) != "" {
		return ClaudeLoginAPIKey, ClaudeSourceAPIKey
	}
	if set("CLAUDE_CODE_OAUTH_TOKEN") {
		return ClaudeLoginAPIKey, ClaudeSourceOAuthToken
	}
	var global struct {
		PrimaryAPIKey string `json:"primaryApiKey"`
	}
	if readClaudeJSON(ClaudeGlobalConfigPath(home, root.Root), &global) && global.PrimaryAPIKey != "" {
		return ClaudeLoginAPIKey, ClaudeSourceAPIKey
	}
	return ClaudeLoginSignedOut, ""
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
