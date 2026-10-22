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
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const claudeHookExec = "exec "

const claudeHookInputLimit = 1 << 20

type claudeHook struct {
	event   string
	args    string
	matcher string
	timeout int64
}

// The prompt hook's timeout is how long, in seconds, Claude lets it hold a
// prompt. The hook ends on its own once the prompt is settled anywhere, so
// the limit only matters while the person is away.
var (
	claudePromptHook   = claudeHook{event: "PermissionRequest", args: "hook permission-request", matcher: "*", timeout: 86400}
	claudeStartHook    = claudeHook{event: "SessionStart", args: "hook session-context", timeout: 10}
	claudePromptSubmit = claudeHook{event: "UserPromptSubmit", args: "hook session-context", timeout: 10}
	claudeHooks        = []claudeHook{claudePromptHook, claudeStartHook, claudePromptSubmit}
)

// claudeSessionContextLimit bounds the session-context hook, registry locks
// included, well inside the hook's timeout: a prompt never waits on it.
const claudeSessionContextLimit = 5 * time.Second

// A daemon restarting, as it does on update, drops the held prompt's
// connection; the hook reconnects for this long so the prompt stays
// answerable from Agenthail.
var (
	claudePromptHookReconnect = 2 * time.Minute
	claudePromptHookRetry     = 2 * time.Second
)

func (a *App) cmdHook(args []string) error {
	if len(args) > 0 && args[0] == "session-context" {
		usage := errors.New("usage: agenthail hook session-context [--surface claude|codex] [--claude-pid <pid>]")
		kind, parent := surface.KindClaude, os.Getppid()
		for rest := args[1:]; len(rest) > 0; rest = rest[2:] {
			if len(rest) < 2 {
				return usage
			}
			switch rest[0] {
			case "--surface":
				kind = surface.SurfaceKind(rest[1])
				if kind != surface.KindClaude && kind != surface.KindCodex {
					return usage
				}
			case "--claude-pid":
				pid, err := strconv.Atoi(rest[1])
				if err != nil || pid <= 0 {
					return usage
				}
				parent = pid
			default:
				return usage
			}
		}
		done := make(chan map[string]any, 1)
		go func() { done <- a.sessionContext(os.Stdin, kind, parent, time.Now()) }()
		select {
		case output := <-done:
			if output != nil {
				_ = json.NewEncoder(os.Stdout).Encode(output)
			}
		case <-time.After(claudeSessionContextLimit):
		}
		return nil
	}
	if len(args) != 1 || args[0] != "permission-request" {
		return errors.New("usage: agenthail hook permission-request|session-context")
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
	if err := json.NewDecoder(io.LimitReader(input, claudeHookInputLimit)).Decode(&request.Prompt); err != nil {
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
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate agenthail binary: %w", err)
	}
	for _, root := range roots {
		path := filepath.Join(root, "settings.json")
		switch args[0] {
		case "install":
			changed, err := installClaudeHooks(path, executable)
			if err != nil {
				return err
			}
			fmt.Printf("%s: %s\n", path, map[bool]string{true: "installed", false: "already installed"}[changed])
		case "uninstall":
			changed, err := uninstallClaudeHooks(path)
			if err != nil {
				return err
			}
			fmt.Printf("%s: %s\n", path, map[bool]string{true: "removed", false: "not installed"}[changed])
		case "status":
			state, err := claudeHooksState(path)
			if err != nil {
				return err
			}
			if state == claudeHookOutdated {
				state += "; run: agenthail hooks install"
			}
			fmt.Printf("%s: %s\n", path, state)
		}
	}
	return nil
}

// command is the hook command that runs executable. Claude runs hook commands
// with /bin/sh -c, and the prompt hook finds its Claude process as its parent;
// exec keeps a shell such as dash, which does not replace itself with a lone
// command, from becoming that parent.
func (h claudeHook) command(executable string) string {
	return claudeHookExec + "'" + strings.ReplaceAll(executable, "'", `'\''`) + "' " + h.args
}

// owns reports whether a hook command is this hook as `hooks install` writes
// it: this executable, or another agenthail executable, quoted as install
// quotes it and followed by exactly the hook's arguments.
func (h claudeHook) owns(command string) bool {
	command = strings.TrimPrefix(command, claudeHookExec)
	if executable, err := os.Executable(); err == nil && command == strings.TrimPrefix(h.command(executable), claudeHookExec) {
		return true
	}
	executable, found := strings.CutSuffix(command, " "+h.args)
	if !found || len(executable) < 2 || executable[0] != '\'' || executable[len(executable)-1] != '\'' {
		return false
	}
	path := strings.ReplaceAll(executable[1:len(executable)-1], `'\''`, "'")
	return filepath.IsAbs(path) && filepath.Base(path) == "agenthail"
}

// installClaudeHooks adds Agenthail's hooks to one settings file, or points
// earlier ones at this binary, and leaves every other setting as written.
func installClaudeHooks(path, executable string) (bool, error) {
	data, mode, err := surface.ReadClaudeSettingsFile(path)
	if err != nil {
		return false, err
	}
	updated := data
	for _, h := range claudeHooks {
		if updated, err = h.install(updated, h.command(executable)); err != nil {
			return false, err
		}
	}
	if string(updated) == string(data) {
		return false, nil
	}
	return true, surface.WriteClaudeSettingsFile(path, updated, mode)
}

// install moves the hook out of an entry whose matcher narrows it, since
// that matcher may be another hook's, and Agenthail's hooks run for all.
func (h claudeHook) install(data []byte, command string) ([]byte, error) {
	data, err := h.remove(data, func(entry gjson.Result) bool { return !matchesAll(entry) })
	if err != nil {
		return nil, err
	}
	found := false
	for i, entry := range gjson.GetBytes(data, "hooks."+h.event).Array() {
		for j, hook := range entry.Get("hooks").Array() {
			if !h.owns(hook.Get("command").String()) {
				continue
			}
			found = true
			prefix := fmt.Sprintf("hooks.%s.%d.hooks.%d.", h.event, i, j)
			if hook.Get("command").String() != command {
				if data, err = sjson.SetBytes(data, prefix+"command", command); err != nil {
					return nil, err
				}
			}
			if hook.Get("timeout").Int() != h.timeout {
				if data, err = sjson.SetBytes(data, prefix+"timeout", h.timeout); err != nil {
					return nil, err
				}
			}
		}
	}
	if found {
		return data, nil
	}
	entry := map[string]any{"hooks": []map[string]any{{"type": "command", "command": command, "timeout": h.timeout}}}
	if h.matcher != "" {
		entry["matcher"] = h.matcher
	}
	return sjson.SetBytes(data, "hooks."+h.event+".-1", entry)
}

func uninstallClaudeHooks(path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	data, mode, err := surface.ReadClaudeSettingsFile(path)
	if err != nil {
		return false, err
	}
	updated := data
	for _, h := range claudeHooks {
		if updated, err = h.remove(updated, func(gjson.Result) bool { return true }); err != nil {
			return false, err
		}
	}
	if string(updated) == string(data) {
		return false, nil
	}
	return true, surface.WriteClaudeSettingsFile(path, updated, mode)
}

func matchesAll(entry gjson.Result) bool {
	matcher := entry.Get("matcher").String()
	return matcher == "" || matcher == "*"
}

func (h claudeHook) remove(data []byte, from func(entry gjson.Result) bool) ([]byte, error) {
	var err error
	entries := gjson.GetBytes(data, "hooks."+h.event).Array()
	for i := len(entries) - 1; i >= 0; i-- {
		if !from(entries[i]) {
			continue
		}
		hooks := entries[i].Get("hooks").Array()
		kept := 0
		for j := len(hooks) - 1; j >= 0; j-- {
			if !h.owns(hooks[j].Get("command").String()) {
				kept++
				continue
			}
			if data, err = sjson.DeleteBytes(data, fmt.Sprintf("hooks.%s.%d.hooks.%d", h.event, i, j)); err != nil {
				return nil, err
			}
		}
		if kept == 0 && len(hooks) > 0 {
			if data, err = sjson.DeleteBytes(data, fmt.Sprintf("hooks.%s.%d", h.event, i)); err != nil {
				return nil, err
			}
		}
	}
	return data, nil
}

// The states of Agenthail's hooks in a settings file. An outdated hook is
// one install wrote before it ran hooks with exec, with another timeout, or
// under a matcher that narrows it; a file with some of the hooks is outdated
// too. Install brings it up to date.
const (
	claudeHookMissing   = "missing"
	claudeHookOutdated  = "outdated"
	claudeHookInstalled = "installed"
)

func claudeHooksState(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return claudeHookMissing, nil
	}
	if err != nil {
		return "", err
	}
	states := map[string]int{}
	for _, h := range claudeHooks {
		states[h.state(data)]++
	}
	switch len(claudeHooks) {
	case states[claudeHookInstalled]:
		return claudeHookInstalled, nil
	case states[claudeHookMissing]:
		return claudeHookMissing, nil
	}
	return claudeHookOutdated, nil
}

func claudeHookState(path string, h claudeHook) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return claudeHookMissing, nil
	}
	if err != nil {
		return "", err
	}
	return h.state(data), nil
}

func (h claudeHook) state(data []byte) string {
	state := claudeHookMissing
	for _, entry := range gjson.GetBytes(data, "hooks."+h.event).Array() {
		for _, hook := range entry.Get("hooks").Array() {
			command := hook.Get("command").String()
			if !h.owns(command) {
				continue
			}
			if matchesAll(entry) && strings.HasPrefix(command, claudeHookExec) && hook.Get("timeout").Int() == h.timeout {
				return claudeHookInstalled
			}
			state = claudeHookOutdated
		}
	}
	return state
}
