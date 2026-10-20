package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

// claudePromptHookTimeout is how long, in seconds, Claude lets the hook hold
// a prompt. The hook ends on its own once the prompt is settled anywhere, so
// the limit only matters while the person is away.
const claudePromptHookTimeout = 86400

const claudePromptHookArgs = "hook permission-request"

const claudePromptHookExec = "exec "

const claudePromptHookInputLimit = 1 << 20

// A daemon restarting, as it does on update, drops the held prompt's
// connection; the hook reconnects for this long so the prompt stays
// answerable from Agenthail.
var (
	claudePromptHookReconnect = 2 * time.Minute
	claudePromptHookRetry     = 2 * time.Second
)

func (a *App) cmdHook(args []string) error {
	if len(args) != 1 || args[0] != "permission-request" {
		return errors.New("usage: agenthail hook permission-request")
	}
	if os.Getenv("CLAUDE_CODE_ENTRYPOINT") != "cli" {
		return nil
	}
	client, err := newDaemonAPIClient()
	if err != nil {
		return nil
	}
	parent := os.Getppid()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cancelWhenOrphaned(ctx, parent, cancel)
	decision := holdClaudePrompt(ctx, client, os.Stdin, parent)
	if decision != nil {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PermissionRequest", "decision": decision}})
	}
	return nil
}

// holdClaudePrompt passes the prompt to the daemon and waits for a person's
// decision. Every failure returns none, which leaves the prompt to Claude's
// own dialog.
func holdClaudePrompt(ctx context.Context, client *daemonAPIClient, input io.Reader, parent int) *surfaces.ClaudePromptDecision {
	var request daemon.ClaudePermissionRequest
	if err := json.NewDecoder(io.LimitReader(input, claudePromptHookInputLimit)).Decode(&request.Prompt); err != nil {
		return nil
	}
	request.PID = parent
	reconnectUntil := time.Now().Add(claudePromptHookReconnect)
	var body []byte
	for {
		var status int
		var err error
		body, status, err = client.call(ctx, http.MethodPost, "/api/v1/claude/permission-request", nil, request, nil)
		if err == nil && status == http.StatusOK {
			break
		}
		if status != 0 || ctx.Err() != nil || time.Now().After(reconnectUntil) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(claudePromptHookRetry):
		}
	}
	var response daemon.ClaudePermissionResponse
	if json.Unmarshal(body, &response) != nil {
		return nil
	}
	return response.Decision
}

// cancelWhenOrphaned ends the hook when Claude exits. Claude does not stop a
// hook whose prompt was answered elsewhere, and the daemon only notices that
// on its own schedule.
func cancelWhenOrphaned(ctx context.Context, parent int, cancel func()) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if os.Getppid() != parent {
				cancel()
				return
			}
		}
	}
}

func (a *App) cmdHooks(args []string) error {
	if len(args) != 1 || (args[0] != "install" && args[0] != "uninstall" && args[0] != "status") {
		return errors.New("usage: agenthail hooks install|uninstall|status")
	}
	home, _ := os.UserHomeDir()
	roots, err := surface.ClaudeConfigRoots(home)
	if err != nil {
		return err
	}
	command, err := claudePromptHookCommand()
	if err != nil {
		return err
	}
	for _, root := range roots {
		path := filepath.Join(root, "settings.json")
		switch args[0] {
		case "install":
			changed, err := installClaudePromptHook(path, command)
			if err != nil {
				return err
			}
			fmt.Printf("%s: %s\n", path, map[bool]string{true: "installed", false: "already installed"}[changed])
		case "uninstall":
			changed, err := uninstallClaudePromptHook(path)
			if err != nil {
				return err
			}
			fmt.Printf("%s: %s\n", path, map[bool]string{true: "removed", false: "not installed"}[changed])
		case "status":
			state, err := claudePromptHookState(path)
			if err != nil {
				return err
			}
			if state == claudePromptHookOutdated {
				state += "; run: agenthail hooks install"
			}
			fmt.Printf("%s: %s\n", path, state)
		}
	}
	return nil
}

func claudePromptHookCommand() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate agenthail binary: %w", err)
	}
	return claudePromptHookCommandFor(executable), nil
}

// claudePromptHookCommandFor is the hook command that runs executable. Claude
// runs hook commands with /bin/sh -c, and the hook finds its Claude process as
// its parent; exec keeps a shell such as dash, which does not replace itself
// with a lone command, from becoming that parent.
func claudePromptHookCommandFor(executable string) string {
	return claudePromptHookExec + "'" + strings.ReplaceAll(executable, "'", `'\''`) + "' " + claudePromptHookArgs
}

// isClaudePromptHook reports whether a hook command is one `hooks install`
// wrote: this executable, or another agenthail executable, quoted as install
// quotes it and followed by exactly the hook's arguments.
func isClaudePromptHook(command string) bool {
	command = strings.TrimPrefix(command, claudePromptHookExec)
	if own, err := claudePromptHookCommand(); err == nil && command == strings.TrimPrefix(own, claudePromptHookExec) {
		return true
	}
	executable, found := strings.CutSuffix(command, " "+claudePromptHookArgs)
	if !found || len(executable) < 2 || executable[0] != '\'' || executable[len(executable)-1] != '\'' {
		return false
	}
	path := strings.ReplaceAll(executable[1:len(executable)-1], `'\''`, "'")
	return filepath.IsAbs(path) && filepath.Base(path) == "agenthail"
}

// installClaudePromptHook adds Agenthail's PermissionRequest hook to one
// settings file, or points an earlier one at this binary, and leaves every
// other setting as written.
func installClaudePromptHook(path, command string) (bool, error) {
	data, mode, err := surface.ReadClaudeSettingsFile(path)
	if err != nil {
		return false, err
	}
	updated := data
	found := false
	for i, entry := range gjson.GetBytes(data, "hooks.PermissionRequest").Array() {
		for j, hook := range entry.Get("hooks").Array() {
			if !isClaudePromptHook(hook.Get("command").String()) {
				continue
			}
			found = true
			prefix := fmt.Sprintf("hooks.PermissionRequest.%d.hooks.%d.", i, j)
			if hook.Get("command").String() != command {
				if updated, err = sjson.SetBytes(updated, prefix+"command", command); err != nil {
					return false, err
				}
			}
			if hook.Get("timeout").Int() != claudePromptHookTimeout {
				if updated, err = sjson.SetBytes(updated, prefix+"timeout", claudePromptHookTimeout); err != nil {
					return false, err
				}
			}
		}
	}
	if !found {
		entry := map[string]any{"matcher": "*", "hooks": []map[string]any{{"type": "command", "command": command, "timeout": claudePromptHookTimeout}}}
		if updated, err = sjson.SetBytes(updated, "hooks.PermissionRequest.-1", entry); err != nil {
			return false, err
		}
	}
	if string(updated) == string(data) {
		return false, nil
	}
	return true, surface.WriteClaudeSettingsFile(path, updated, mode)
}

func uninstallClaudePromptHook(path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	data, mode, err := surface.ReadClaudeSettingsFile(path)
	if err != nil {
		return false, err
	}
	updated := data
	entries := gjson.GetBytes(data, "hooks.PermissionRequest").Array()
	for i := len(entries) - 1; i >= 0; i-- {
		hooks := entries[i].Get("hooks").Array()
		kept := 0
		for j := len(hooks) - 1; j >= 0; j-- {
			if !isClaudePromptHook(hooks[j].Get("command").String()) {
				kept++
				continue
			}
			if updated, err = sjson.DeleteBytes(updated, fmt.Sprintf("hooks.PermissionRequest.%d.hooks.%d", i, j)); err != nil {
				return false, err
			}
		}
		if kept == 0 && len(hooks) > 0 {
			if updated, err = sjson.DeleteBytes(updated, fmt.Sprintf("hooks.PermissionRequest.%d", i)); err != nil {
				return false, err
			}
		}
	}
	if string(updated) == string(data) {
		return false, nil
	}
	return true, surface.WriteClaudeSettingsFile(path, updated, mode)
}

// The states of the prompt hook in a settings file. An outdated hook is one
// install wrote before it ran the hook with exec, or with another timeout;
// install brings it up to date.
const (
	claudePromptHookMissing   = "missing"
	claudePromptHookOutdated  = "outdated"
	claudePromptHookInstalled = "installed"
)

func claudePromptHookState(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return claudePromptHookMissing, nil
	}
	if err != nil {
		return "", err
	}
	state := claudePromptHookMissing
	for _, entry := range gjson.GetBytes(data, "hooks.PermissionRequest").Array() {
		for _, hook := range entry.Get("hooks").Array() {
			command := hook.Get("command").String()
			if !isClaudePromptHook(command) {
				continue
			}
			if strings.HasPrefix(command, claudePromptHookExec) && hook.Get("timeout").Int() == claudePromptHookTimeout {
				return claudePromptHookInstalled, nil
			}
			state = claudePromptHookOutdated
		}
	}
	return state, nil
}
