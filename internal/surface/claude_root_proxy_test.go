package surface

import (
	"path/filepath"
	"testing"
)

func TestARootIsViaAProxyByItsBaseURLOrByName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	byURL, byName, other := filepath.Join(home, ".a", ".claude"), filepath.Join(home, ".b", ".claude"), filepath.Join(home, ".c", ".claude")
	writeClaudeGlobalConfigFixture(t, AgenthailConfigPath(home), map[string]any{
		"claudeConfigDirs": []any{byURL, map[string]any{"path": byName, "proxy": "shared"}, other},
		"proxies":          []any{map[string]any{"name": "shared", "url": "http://127.0.0.1:8317"}},
	})
	writeClaudeGlobalConfigFixture(t, filepath.Join(byURL, "settings.json"), map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "http://localhost:8317/", "ANTHROPIC_AUTH_TOKEN": "x"}})
	writeClaudeGlobalConfigFixture(t, filepath.Join(other, "settings.json"), map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "http://gateway.example:9000", "ANTHROPIC_AUTH_TOKEN": "x"}})
	roots, err := DescribeClaudeConfigRoots(home)
	if err != nil || len(roots) != 4 {
		t.Fatalf("roots=%+v err=%v", roots, err)
	}
	for i, want := range []string{"", "shared", "shared", ""} {
		if roots[i].Proxy != want {
			t.Fatalf("root %s proxy=%q, want %q", roots[i].Root, roots[i].Proxy, want)
		}
	}
	if login := ClaudeRootLogin(home, roots[1]); login != ClaudeLoginProxy {
		t.Fatalf("a proxied root is not an API key: %s", login)
	}
	if login := ClaudeRootLogin(home, roots[3]); login != ClaudeLoginAPIKey {
		t.Fatalf("a base URL no proxy is configured for stays an API key: %s", login)
	}
}

func TestTheShellsBaseURLPutsEveryRootWithoutItsOwnViaTheProxy(t *testing.T) {
	home := claudeRootsHome(t, ".work/.claude")
	t.Setenv("ANTHROPIC_BASE_URL", "http://[::1]:8317")
	writeClaudeGlobalConfigFixture(t, AgenthailConfigPath(home), map[string]any{
		"claudeConfigDirs": []string{filepath.Join(home, ".work", ".claude")},
		"proxies":          []any{map[string]any{"name": "local", "url": "http://localhost:8317"}},
	})
	roots, _ := DescribeClaudeConfigRoots(home)
	if roots[0].Proxy != "local" || roots[1].Proxy != "local" {
		t.Fatalf("roots=%+v", roots)
	}
}

func TestABrokenProxiesEntryNeverHidesTheRoots(t *testing.T) {
	home := claudeRootsHome(t, ".work/.claude")
	writeClaudeGlobalConfigFixture(t, AgenthailConfigPath(home), map[string]any{
		"claudeConfigDirs": []string{filepath.Join(home, ".work", ".claude")},
		"proxies":          "not a list",
	})
	roots, err := DescribeClaudeConfigRoots(home)
	if err != nil || len(roots) != 2 || roots[1].Proxy != "" {
		t.Fatalf("roots=%+v err=%v", roots, err)
	}
	if _, err := ConfiguredProxies(home); err == nil {
		t.Fatal("the proxies error is reported where proxies are read")
	}
}
