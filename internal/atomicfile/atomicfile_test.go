package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReplacesTheFileWithTheGivenMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, write := range []func(string, []byte, os.FileMode) error{Write, WriteSynced} {
		if err := write(path, []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "new" {
			t.Fatalf("content = %q, %v", data, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, %v", info.Mode().Perm(), err)
		}
	}
	assertOnly(t, filepath.Dir(path), "settings.json")
}

func TestAFailedWriteKeepsTheOldFileAndLeavesNoTemporary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	failing := io.MultiReader(strings.NewReader("partial"), errorReader{errors.New("decoder failed")})
	if err := WriteFrom(path, failing, 0o600); err == nil {
		t.Fatal("a failing reader was published")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old" {
		t.Fatalf("content = %q, %v", data, err)
	}
	assertOnly(t, filepath.Dir(path), "rollout.jsonl")
}

func assertOnly(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("directory holds %v, want only %s", names, name)
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }
