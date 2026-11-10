package testnode

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func ClaudeHost(t testing.TB) string {
	t.Helper()
	return hostBundle(t, "claude-host", "@anthropic-ai/claude-agent-sdk")
}

func BrowserHost(t testing.TB) string {
	t.Helper()
	return hostBundle(t, "browser-host", "playwright-core")
}

// A host without its packages exits at import, which a test would otherwise see only as a start timeout.
func hostBundle(t testing.TB, bundle, dependency string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", bundle)
	if _, err := os.Stat(filepath.Join(dir, "node_modules", dependency, "package.json")); err != nil {
		t.Fatalf("tests run the %s with its dependencies; install them with npm ci --prefix %s --omit=optional --ignore-scripts: %v", bundle, bundle, err)
	}
	return dir
}
