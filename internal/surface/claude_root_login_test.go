package surface

import (
	"path/filepath"
	"strings"
	"testing"
)

var claudeLoginEnvNames = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD", "CLAUDE_CODE_USE_MANTLE"}

func claudeLoginRoot(t *testing.T) (string, ClaudeConfigRoot) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, name := range claudeLoginEnvNames {
		t.Setenv(name, "")
	}
	return home, ClaudeConfigRoot{Name: "default", Root: filepath.Join(home, ".claude")}
}

func TestEachWayClaudeSignsInIsRecognisedFromTheEnvironmentItIsLaunchedWith(t *testing.T) {
	for _, path := range []struct{ name, value, source string }{
		{"ANTHROPIC_API_KEY", "sk-test", ClaudeSourceAPIKey},
		{"ANTHROPIC_AUTH_TOKEN", "token", ClaudeSourceAPIKey},
		{"CLAUDE_CODE_OAUTH_TOKEN", "oauth", ClaudeSourceOAuthToken},
		{"CLAUDE_CODE_USE_BEDROCK", "1", ClaudeSourceBedrock},
		{"CLAUDE_CODE_USE_VERTEX", "true", ClaudeSourceVertex},
		{"CLAUDE_CODE_USE_FOUNDRY", "yes", ClaudeSourceFoundry},
		{"CLAUDE_CODE_USE_ANTHROPIC_AWS", "on", ClaudeSourceCloudProvider},
		{"CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD", "TRUE", ClaudeSourceCloudProvider},
		{"CLAUDE_CODE_USE_MANTLE", "1", ClaudeSourceCloudProvider},
	} {
		t.Run(path.name, func(t *testing.T) {
			home, root := claudeLoginRoot(t)
			t.Setenv(path.name, path.value)
			if login, source := ClaudeRootLogin(home, root); login != ClaudeLoginAPIKey || source != path.source {
				t.Fatalf("%s set: login=%q source=%q, want api_key %q", path.name, login, source, path.source)
			}
		})
	}
}

func TestAKeyStoredInTheRootsGlobalConfigSignsItIn(t *testing.T) {
	home, root := claudeLoginRoot(t)
	writeClaudeGlobalConfigFixture(t, ClaudeGlobalConfigPath(home, root.Root), map[string]any{"primaryApiKey": "sk-stored"})
	if login, source := ClaudeRootLogin(home, root); login != ClaudeLoginAPIKey || source != ClaudeSourceAPIKey {
		t.Fatalf("a stored key: login=%q source=%q", login, source)
	}
}

func TestARootsSettingsEnvSignsItInAndOverridesTheHostsEnvironment(t *testing.T) {
	home, root := claudeLoginRoot(t)
	writeClaudeGlobalConfigFixture(t, filepath.Join(root.Root, "settings.json"), map[string]any{"env": map[string]any{"CLAUDE_CODE_USE_VERTEX": "1", "CLAUDE_CODE_OAUTH_TOKEN": "x"}})
	if login, source := ClaudeRootLogin(home, root); login != ClaudeLoginAPIKey || source != ClaudeSourceVertex {
		t.Fatalf("settings env with Vertex: login=%q source=%q", login, source)
	}
	writeClaudeGlobalConfigFixture(t, filepath.Join(root.Root, "settings.json"), map[string]any{"env": map[string]any{"CLAUDE_CODE_OAUTH_TOKEN": "x"}})
	if login, source := ClaudeRootLogin(home, root); login != ClaudeLoginAPIKey || source != ClaudeSourceOAuthToken {
		t.Fatalf("settings env with an OAuth token: login=%q source=%q", login, source)
	}
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	writeClaudeGlobalConfigFixture(t, filepath.Join(root.Root, "settings.json"), map[string]any{"env": map[string]any{"CLAUDE_CODE_USE_BEDROCK": "0"}})
	if login, _ := ClaudeRootLogin(home, root); login != ClaudeLoginSignedOut {
		t.Fatalf("a root whose settings turn Bedrock off still read %q", login)
	}
}

func TestACloudProviderOutranksTheRootsClaudeAccount(t *testing.T) {
	home, root := claudeLoginRoot(t)
	root.SignedInAs = &ClaudeLogin{Email: "dev@example.com", AccountID: "acct"}
	if login, _ := ClaudeRootLogin(home, root); login != ClaudeLoginClaudeAI {
		t.Fatalf("a signed-in root read %q", login)
	}
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	if login, source := ClaudeRootLogin(home, root); login != ClaudeLoginAPIKey || source != ClaudeSourceBedrock {
		t.Fatalf("a signed-in root that Claude sends to Bedrock: login=%q source=%q", login, source)
	}
}

func TestARootClaudeCannotAuthenticateWithStaysSignedOut(t *testing.T) {
	home, root := claudeLoginRoot(t)
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "0")
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "false")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "  ")
	writeClaudeGlobalConfigFixture(t, filepath.Join(root.Root, "settings.json"), map[string]any{"env": map[string]any{"CLAUDE_CODE_USE_FOUNDRY": ""}})
	if login, source := ClaudeRootLogin(home, root); login != ClaudeLoginSignedOut || source != "" {
		t.Fatalf("login=%q source=%q, want signed_out", login, source)
	}
}

func TestALoginIsNamedForWhatItSignsInWithAndNeverForItsCredential(t *testing.T) {
	home, root := claudeLoginRoot(t)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat-secret")
	login, source := ClaudeRootLogin(home, root)
	if title := ClaudeLoginTitle(login, source); title != "OAuth token" {
		t.Fatalf("title=%q", title)
	}
	for _, text := range []string{login, source, ClaudeLoginTitle(login, source), ClaudeLoginPhrase(source)} {
		if strings.Contains(text, "secret") {
			t.Fatalf("a credential reached the label: %q", text)
		}
	}
	if ClaudeLoginTitle(ClaudeLoginAPIKey, "") != "API key" || ClaudeLoginTitle(ClaudeLoginSignedOut, "") != "signed out" || ClaudeLoginTitle(ClaudeLoginClaudeAI, "") != "" {
		t.Fatal("the plain logins keep their names")
	}
}
