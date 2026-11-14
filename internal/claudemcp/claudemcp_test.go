package claudemcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestMain(m *testing.M) {
	os.Exit(testkit.RunInOwnHome(m.Run))
}

const othersConfig = `{
  "numStartups": 12,
  "mcpServers": {
    "github": {
      "type": "http",
      "url": "https://example.invalid/mcp",
      "headers": { "Authorization": "Bearer x" }
    },
    "files": { "type": "stdio", "command": "npx", "args": ["-y", "files-server"], "env": { "ROOT": "/srv" } }
  },
  "projects": { "/work": { "hasTrustDialogAccepted": true } }
}
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestInstallAddsOnlyItsOwnServerAndASecondInstallChangesNothing(t *testing.T) {
	path := writeConfig(t, othersConfig)
	executable := filepath.Join(t.TempDir(), "agenthail")
	if err := os.WriteFile(executable, nil, 0700); err != nil {
		t.Fatal(err)
	}
	if changed, err := InstallAt(path, executable); err != nil || !changed {
		t.Fatalf("install = %v, %v", changed, err)
	}
	installed := read(t, path)
	for _, other := range []string{"numStartups", "mcpServers.github", "mcpServers.files", "projects"} {
		if gjson.GetBytes(installed, other).Raw != gjson.Get(othersConfig, other).Raw {
			t.Fatalf("install changed %s:\n%s", other, installed)
		}
	}
	if state, err := StateAt(path, executable); err != nil || state != Installed {
		t.Fatalf("state = %q, %v", state, err)
	}
	if changed, err := InstallAt(path, executable); err != nil || changed || !bytes.Equal(read(t, path), installed) {
		t.Fatalf("a second install = %v, %v and rewrote the file", changed, err)
	}
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if state, err := StateAt(path, executable); err != nil || state != Dangling {
		t.Fatalf("with its command gone, state = %q, %v", state, err)
	}
	if changed, err := InstallAt(path, "/opt/agenthail/bin/agenthail"); err != nil || !changed || gjson.GetBytes(read(t, path), key+".command").String() != "/opt/agenthail/bin/agenthail" {
		t.Fatalf("install from another executable = %v, %v", changed, err)
	}
	if changed, err := UninstallAt(path); err != nil || !changed || string(read(t, path)) != othersConfig {
		t.Fatalf("uninstall = %v, %v, left:\n%s", changed, err, read(t, path))
	}
	if changed, err := UninstallAt(path); err != nil || changed {
		t.Fatalf("a second uninstall = %v, %v", changed, err)
	}
}

func TestAServerNamedAgenthailThatInstallDidNotWriteIsLeftAlone(t *testing.T) {
	for name, own := range map[string]string{
		"http":                   `{"mcpServers":{"agenthail":{"type":"http","url":"http://127.0.0.1:9/mcp"}}}`,
		"the relay by hand":      `{"mcpServers":{"agenthail":{"type":"stdio","command":"/usr/local/bin/agenthail","args":["mcp","stdio"]}}}`,
		"the relay with its env": `{"mcpServers":{"agenthail":{"type":"stdio","command":"/usr/local/bin/agenthail","args":["mcp","stdio"],"env":{"X":"1"}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, own)
			if _, err := InstallAt(path, "/usr/local/bin/agenthail"); err == nil {
				t.Fatal("install replaced a server it did not add")
			}
			if changed, err := UninstallAt(path); err != nil || changed {
				t.Fatalf("uninstall = %v, %v", changed, err)
			}
			if string(read(t, path)) != own {
				t.Fatalf("the person's own server changed:\n%s", read(t, path))
			}
			if state, _ := StateAt(path, "/usr/local/bin/agenthail"); state != Taken {
				t.Fatalf("state = %q", state)
			}
		})
	}
}

func claudeWrites(t *testing.T, path string, times int) {
	dir := path + ".lock"
	for i := range times {
		for os.Mkdir(dir, 0700) != nil {
			time.Sleep(time.Millisecond)
		}
		var config map[string]any
		if err := json.Unmarshal(read(t, path), &config); err != nil {
			t.Error(err)
		}
		config["numStartups"] = config["numStartups"].(float64) + 1
		config["projects"].(map[string]any)["/work/"+strconv.Itoa(i)] = map[string]any{}
		data, _ := json.MarshalIndent(config, "", "  ")
		if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
			t.Error(err)
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			t.Error(err)
		}
		_ = os.Remove(dir)
	}
}

func TestInstallAndClaudesOwnWritesToTheSameFileBothLand(t *testing.T) {
	path := writeConfig(t, othersConfig)
	const writes = 300
	var wg sync.WaitGroup
	wg.Go(func() { claudeWrites(t, path, writes) })
	executables := []string{"/usr/local/bin/agenthail", "/opt/agenthail/bin/agenthail"}
	for i := range 60 {
		if _, err := InstallAt(path, executables[i%2]); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	data := read(t, path)
	if got := gjson.GetBytes(data, "numStartups").Int(); got != 12+writes {
		t.Fatalf("Claude's writes were lost: numStartups = %d, want %d", got, 12+writes)
	}
	if got := gjson.GetBytes(data, key+".command").String(); got != executables[1] {
		t.Fatalf("the last install was lost: command = %q", got)
	}
	if !gjson.GetBytes(data, "mcpServers.github").Exists() || !gjson.GetBytes(data, "mcpServers.files").Exists() {
		t.Fatalf("other servers were lost:\n%s", data)
	}
}

func TestInstallWaitsForClaudesLockAndTakesOverOneLeftByADeadProcess(t *testing.T) {
	path := writeConfig(t, othersConfig)
	dir := path + ".lock"
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	held := 300 * time.Millisecond
	go func() {
		time.Sleep(held)
		_ = os.Remove(dir)
	}()
	start := time.Now()
	if _, err := InstallAt(path, "/usr/local/bin/agenthail"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < held {
		t.Fatalf("install wrote after %s while Claude held the lock", elapsed)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallAt(path, "/opt/agenthail/bin/agenthail"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("install left the lock behind: %v", err)
	}
}

func TestALockInstallHoldsStaysFreshPastTheStaleAge(t *testing.T) {
	stale := lockStale
	lockStale = 200 * time.Millisecond
	t.Cleanup(func() { lockStale = stale })
	path := writeConfig(t, othersConfig)
	release, err := lock(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * lockStale)
	info, err := os.Stat(path + ".lock")
	if err != nil || time.Since(info.ModTime()) > lockStale {
		t.Fatalf("a lock held past the stale age looks abandoned: %v, %v", info, err)
	}
	if os.Mkdir(path+".lock", 0700) == nil {
		t.Fatal("another writer took a lock install holds")
	}
	release()
	if _, err := os.Stat(path + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release left the lock: %v", err)
	}
}
