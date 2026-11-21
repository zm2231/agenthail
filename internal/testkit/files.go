package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func AppendFile(t testing.TB, path string, content ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(content, "")); err != nil {
		t.Fatal(err)
	}
}

func AppendLines(t testing.TB, path string, lines ...string) {
	t.Helper()
	var content strings.Builder
	for _, line := range lines {
		content.WriteString(line + "\n")
	}
	AppendFile(t, path, content.String())
}

func WriteExecutable(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}

// rewriteKeepingTime writes content over path and puts its modification time
// back to at, as a same-size rewrite inside one timestamp tick leaves it.
func RewriteKeepingTime(t testing.TB, path, content string, at time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}
