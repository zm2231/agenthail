// Package claudemcp keeps Agenthail's MCP server in a Claude config root's
// user scope, so every Claude session started from that root, in a terminal
// or anywhere else, gets the Agenthail tools as itself.
package claudemcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	Name = "agenthail"
	key  = "mcpServers." + Name
)

// Taken is an entry named agenthail that install did not write; it is left
// alone. Dangling is one of install's whose command no longer exists.
const (
	Missing   = "missing"
	Outdated  = "outdated"
	Installed = "installed"
	Taken     = "taken"
	Dangling  = "dangling"
)

// installedBy marks the entry install writes, so a server a person added is
// never taken for it, whatever its shape.
const installedBy = "AGENTHAIL_MCP_INSTALLED_BY"

func entry(executable string) []byte {
	data, _ := json.Marshal(map[string]any{"type": "stdio", "command": executable, "args": []string{"mcp", "stdio"}, "env": map[string]string{installedBy: "agenthail mcp install"}})
	return data
}

func owns(server gjson.Result) bool {
	return server.Get("env").Get(installedBy).Exists()
}

func state(data []byte, executable string) string {
	server := gjson.GetBytes(data, key)
	switch {
	case !server.Exists():
		return Missing
	case !owns(server):
		return Taken
	case !exists(server.Get("command").String()):
		return Dangling
	case !sameJSON(server.Raw, entry(executable)):
		return Outdated
	}
	return Installed
}

func sameJSON(a string, b []byte) bool {
	var left, right bytes.Buffer
	return json.Compact(&left, []byte(a)) == nil && json.Compact(&right, b) == nil && bytes.Equal(left.Bytes(), right.Bytes())
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func StateAt(path, executable string) (string, error) {
	data, _, err := surface.ReadClaudeSettingsFile(path)
	if err != nil {
		return "", err
	}
	return state(data, executable), nil
}

func InstallAt(path, executable string) (bool, error) {
	return update(path, func(data []byte) ([]byte, error) {
		switch state(data, executable) {
		case Installed:
			return data, nil
		case Taken:
			return nil, fmt.Errorf("%s already has an MCP server named %s that Agenthail did not add; rename or remove it, then install again", path, Name)
		}
		return sjson.SetRawBytes(data, key, entry(executable))
	})
}

func UninstallAt(path string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return update(path, func(data []byte) ([]byte, error) {
		if server := gjson.GetBytes(data, key); !server.Exists() || !owns(server) {
			return data, nil
		}
		return sjson.DeleteBytes(data, key)
	})
}

// update changes the file under the lock Claude takes for its own writes, so
// neither side writes over the other; Claude reads the file afresh under it.
func update(path string, change func([]byte) ([]byte, error)) (bool, error) {
	data, _, err := surface.ReadClaudeSettingsFile(path)
	if err != nil {
		return false, err
	}
	if updated, err := change(bytes.Clone(data)); err != nil || bytes.Equal(updated, data) {
		return false, err
	}
	release, err := lock(path)
	if err != nil {
		return false, err
	}
	defer release()
	data, mode, err := surface.ReadClaudeSettingsFile(path)
	if err != nil {
		return false, err
	}
	updated, err := change(bytes.Clone(data))
	if err != nil || bytes.Equal(updated, data) {
		return false, err
	}
	return true, surface.WriteClaudeSettingsFile(path, updated, mode)
}

// Claude locks its config as proper-lockfile does: a directory beside the
// file whose modification time the holder refreshes every lockStale/2; one
// left unrefreshed past lockStale belongs to a process that died holding it.
var (
	lockStale = 10 * time.Second
	lockWait  = 15 * time.Second
)

func lock(path string) (func(), error) {
	dir := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for wait := 10 * time.Millisecond; ; wait = min(wait*2, 200*time.Millisecond) {
		err := os.Mkdir(dir, 0700)
		if err == nil {
			return hold(dir), nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(dir); statErr == nil && time.Since(info.ModTime()) > lockStale {
			_ = os.Remove(dir)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s stayed locked by Claude for %s; try again", path, lockWait)
		}
		time.Sleep(wait)
	}
}

func hold(dir string) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(lockStale / 2)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				_ = os.Chtimes(dir, now, now)
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		_ = os.Remove(dir)
	}
}
