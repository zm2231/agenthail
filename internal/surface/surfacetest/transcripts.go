package surfacetest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func AppendTranscript(t *testing.T, path, lines string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(lines); err != nil {
		t.Fatal(err)
	}
}

func ReplaceTranscript(t *testing.T, path string, records ...string) {
	t.Helper()
	replacement := path + ".next"
	if err := os.WriteFile(replacement, []byte(strings.Join(records, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
}

func AppendLine(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

func TimelineFixture(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func WriteTestTranscript(t *testing.T, path string, records ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(stringsJoinLines(records)), 0600); err != nil {
		t.Fatal(err)
	}
}

func stringsJoinLines(records []string) string {
	result := ""
	for _, record := range records {
		result += record + "\n"
	}
	return result
}
