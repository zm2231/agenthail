package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface"
)

const cmuxSettings = `{
  "model": "opus",
  "hooks": {
    "PermissionRequest": [
      {"matcher": "*", "hooks": [{"type": "command", "command": "cmux-hook permission", "timeout": 125}]},
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/agenthail-wrapper hook permission-request"}]}
    ],
    "Stop": [{"hooks": [{"type": "command", "command": "notify-done"}]}]
  },
  "permissions": {"allow": ["Bash(ls)"]}
}
`

func TestHooksInstallAddsAgenthailsHooksToEveryClaudeRootAndKeepsOtherSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	second := filepath.Join(home, "claude-work")
	for _, dir := range []string{filepath.Join(home, ".claude"), second, filepath.Join(home, ".agenthail")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(`{"claudeConfigDirs":["`+second+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(primary, []byte(cmuxSettings), 0640); err != nil {
		t.Fatal(err)
	}
	app, _ := cliFixture(t, &cliSurface{kind: "claude"})
	doctorHook := func() map[string]string {
		t.Helper()
		output, _ := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
		var report struct {
			ClaudeHooks map[string]string `json:"claudeHooks"`
		}
		if err := json.Unmarshal([]byte(output), &report); err != nil {
			t.Fatalf("output=%s err=%v", output, err)
		}
		return report.ClaudeHooks
	}
	if hook := doctorHook(); hook[filepath.Join(home, ".claude")] != claudeHookMissing || hook[second] != claudeHookMissing || len(hook) != 2 {
		t.Fatalf("doctor before install=%v", hook)
	}
	for range 2 {
		if _, err := captureStdout(t, func() error { return app.Run([]string{"hooks", "install"}) }); err != nil {
			t.Fatal(err)
		}
	}
	if hook := doctorHook(); hook[filepath.Join(home, ".claude")] != claudeHookInstalled || hook[second] != claudeHookInstalled {
		t.Fatalf("doctor after install=%v", hook)
	}
	data, err := os.ReadFile(primary)
	if err != nil {
		t.Fatal(err)
	}
	entries := gjson.GetBytes(data, "hooks.PermissionRequest").Array()
	if len(entries) != 3 || entries[0].Get("hooks.0.command").String() != "cmux-hook permission" || entries[1].Get("hooks.0.command").String() != "/usr/local/bin/agenthail-wrapper hook permission-request" {
		t.Fatalf("a repeated install did not keep one hook beside the others: %s", data)
	}
	ours := entries[2].Get("hooks.0")
	if !strings.HasPrefix(ours.Get("command").String(), "exec '") || !strings.HasSuffix(ours.Get("command").String(), "' hook permission-request") || ours.Get("timeout").Int() != claudePromptHook.timeout || entries[2].Get("matcher").String() != "*" {
		t.Fatalf("hook=%s", ours.Raw)
	}
	for _, h := range []claudeHook{claudeStartHook, claudePromptSubmit} {
		if entries := gjson.GetBytes(data, "hooks."+h.event).Array(); len(entries) != 1 || !strings.HasSuffix(entries[0].Get("hooks.0.command").String(), "' hook session-context") || entries[0].Get("matcher").Exists() {
			t.Fatalf("%s hooks=%s", h.event, gjson.GetBytes(data, "hooks."+h.event).Raw)
		}
	}
	if gjson.GetBytes(data, "model").String() != "opus" || gjson.GetBytes(data, "permissions.allow.0").String() != "Bash(ls)" || gjson.GetBytes(data, "hooks.Stop.0.hooks.0.command").String() != "notify-done" {
		t.Fatalf("other settings changed: %s", data)
	}
	if info, err := os.Stat(primary); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if _, err := os.Stat(filepath.Join(second, "settings.json")); err != nil {
		t.Fatalf("the configured root got no settings: %v", err)
	}

	if _, err := captureStdout(t, func() error { return app.Run([]string{"hooks", "uninstall"}) }); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(primary)
	if err != nil {
		t.Fatal(err)
	}
	if entries := gjson.GetBytes(data, "hooks.PermissionRequest").Array(); len(entries) != 2 || entries[0].Get("hooks.0.command").String() != "cmux-hook permission" || entries[1].Get("hooks.0.command").String() != "/usr/local/bin/agenthail-wrapper hook permission-request" {
		t.Fatalf("uninstall touched another hook or left ours: %s", data)
	}
	if gjson.GetBytes(data, "hooks.SessionStart.#").Int() != 0 || gjson.GetBytes(data, "hooks.UserPromptSubmit.#").Int() != 0 {
		t.Fatalf("uninstall left the session-context hooks: %s", data)
	}
	if hook := doctorHook(); hook[filepath.Join(home, ".claude")] != claudeHookMissing || hook[second] != claudeHookMissing {
		t.Fatalf("doctor after uninstall=%v", hook)
	}
}

func TestHooksInstallRefusesSettingsThatAreNotAJSONObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"model": `), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := installClaudeHooks(path, "/bin/agenthail"); err == nil {
		t.Fatal("a broken settings file was rewritten")
	}
	if data, _ := os.ReadFile(path); string(data) != `{"model": ` {
		t.Fatalf("settings=%s", data)
	}
}

func TestAnswerReachesAPromptTheDaemonHoldsForATerminalSession(t *testing.T) {
	fake := &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, caps: surface.Capabilities{Send: true, Reply: true}}
	app, reg := cliFixture(t, fake)
	session := surface.Session{ID: "terminal-1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "terminal work"}
	fake.sessions[session.ID] = session
	if err := reg.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetAlias("terminal", session.ID); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var answered []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/requests":
			if r.URL.Query().Get("sessionId") != session.ID {
				http.Error(w, "unknown session", http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, `{"requests":[{"id":"terminal-1:question:held:1","kind":"question","tool":"AskUserQuestion","createdAt":"2026-10-20T10:00:00Z","questions":[{"question":"Which branch?","header":"Branch","options":[{"label":"main","description":""},{"label":"next","description":""}],"multiSelect":false}]}]}`)
		case "/api/v1/actions":
			var action map[string]any
			_ = json.NewDecoder(r.Body).Decode(&action)
			mu.Lock()
			answered = append(answered, action)
			stale := len(answered) > 1
			mu.Unlock()
			if stale {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"ok":false,"error":{"code":"stale","message":"stale"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":{"requestId":"terminal-1:question:held:1","outcome":"answered"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"requests", "@terminal"}) })
	if err != nil || !strings.Contains(output, "Which branch?") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"answer", "@terminal", "next"}) })
	if err != nil || !strings.Contains(output, "answered") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	mu.Lock()
	first := answered[0]
	mu.Unlock()
	decision, _ := first["decision"].(map[string]any)
	answers, _ := decision["answers"].(map[string]any)
	if first["action"] != "respond" || first["sessionId"] != session.ID || first["requestId"] != "terminal-1:question:held:1" || answers["Which branch?"] != "next" {
		t.Fatalf("action=%v", first)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"answer", "@terminal", "next"}) }); err == nil || !strings.Contains(err.Error(), surface.ErrRequestStale.Error()) {
		t.Fatalf("a second answer err=%v", err)
	}
}

func TestThePromptHookOutlivesADaemonRestartAndStopsOnARefusal(t *testing.T) {
	previous := claudePromptHookRetry
	claudePromptHookRetry = 10 * time.Millisecond
	t.Cleanup(func() { claudePromptHookRetry = previous })
	var mu sync.Mutex
	var calls []map[string]any
	refuse := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		mu.Lock()
		calls = append(calls, request)
		count, refused := len(calls), refuse
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer dashboard-synthetic" || r.URL.Path != "/api/v1/claude/permission-request" {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		if refused {
			http.Error(w, `{"error":{"code":"not_held"}}`, http.StatusConflict)
			return
		}
		if count == 1 {
			hijacked, _, _ := w.(http.Hijacker).Hijack()
			hijacked.Close()
			return
		}
		_, _ = io.WriteString(w, `{"decision":{"behavior":"allow","updatedInput":{"command":"ls"}}}`)
	}))
	t.Cleanup(server.Close)
	client := &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}
	prompt := `{"session_id":"terminal-1","tool_name":"Bash","tool_input":{"command":"ls"},"permission_suggestions":[]}`
	decision := holdClaudePrompt(context.Background(), client, strings.NewReader(prompt), 4242)
	if decision == nil || decision.Behavior != "allow" || decision.UpdatedInput["command"] != "ls" {
		t.Fatalf("decision=%+v", decision)
	}
	mu.Lock()
	if len(calls) != 2 || calls[1]["pid"] != float64(4242) || calls[1]["prompt"].(map[string]any)["session_id"] != "terminal-1" {
		t.Fatalf("calls=%v", calls)
	}
	refuse = true
	calls = nil
	mu.Unlock()
	if decision := holdClaudePrompt(context.Background(), client, strings.NewReader(prompt), 4242); decision != nil {
		t.Fatalf("a refused prompt got decision %+v", decision)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("a refusal was retried %d times", len(calls))
	}
}

func TestOnlyTheHookCommandInstallWritesIsAgenthails(t *testing.T) {
	for command, ours := range map[string]bool{
		"'/usr/local/bin/agenthail' hook permission-request":                         true,
		"exec '/usr/local/bin/agenthail' hook permission-request":                    true,
		"exec /usr/local/bin/agenthail hook permission-request":                      false,
		"exec 'agenthail' hook permission-request":                                   false,
		`'/Library/Application Support/Agenthail/agenthail' hook permission-request`: true,
		`'/tmp/it'\''s/agenthail' hook permission-request`:                           true,
		"/usr/local/bin/agenthail-wrapper hook permission-request":                   false,
		"'/usr/local/bin/agenthail-wrapper' hook permission-request":                 false,
		"agenthail hook permission-request":                                          false,
		"'/usr/local/bin/agenthail' hook permission-request --verbose":               false,
		"'agenthail' hook permission-request":                                        false,
	} {
		if claudePromptHook.owns(command) != ours {
			t.Errorf("owns(%q) != %v", command, ours)
		}
	}
	if claudeStartHook.owns("exec '/usr/local/bin/agenthail' hook permission-request") || !claudeStartHook.owns("exec '/usr/local/bin/agenthail' hook session-context") {
		t.Error("the session-context hook claims the prompt hook's command or misses its own")
	}
}

// Claude runs a hook command with /bin/sh -c, and the hook takes its parent
// for the Claude process asking. A shell that does not replace itself with a
// lone command, such as dash, would be that parent instead.
func TestTheHookCommandRunsWithClaudeAsItsParentUnderEveryShell(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "it's here")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	recorded := filepath.Join(dir, "parent")
	stand := filepath.Join(dir, "agenthail")
	if err := os.WriteFile(stand, []byte("#!/bin/sh\necho \"$PPID $*\" > \""+recorded+"\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command := claudePromptHook.command(stand)
	if !claudePromptHook.owns(command) {
		t.Fatalf("the command install writes is not recognized as Agenthail's: %q", command)
	}
	ran := 0
	for _, shell := range []string{"/bin/sh", "/bin/dash", "/bin/bash"} {
		if _, err := os.Stat(shell); err != nil {
			continue
		}
		ran++
		os.Remove(recorded)
		if output, err := exec.Command(shell, "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("%s -c %q = %v: %s", shell, command, err, output)
		}
		data, err := os.ReadFile(recorded)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.TrimSpace(string(data)), fmt.Sprintf("%d %s", os.Getpid(), claudePromptHook.args); got != want {
			t.Errorf("under %s the hook saw parent and args %q, want %q", shell, got, want)
		}
	}
	if ran == 0 {
		t.Skip("no shell to run the hook command with")
	}
}

func TestInstallBringsAHookWrittenWithoutExecUpToDate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := claudePromptHook.command(executable)
	old := strings.TrimPrefix(command, claudeHookExec)
	if err := os.WriteFile(settings, []byte(`{"hooks":{"PermissionRequest":[{"matcher":"*","hooks":[{"type":"command","command":`+strconv.Quote(old)+`,"timeout":86400}]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	app, _ := cliFixture(t, &cliSurface{kind: "claude"})
	status := func() string {
		t.Helper()
		output, err := captureStdout(t, func() error { return app.Run([]string{"hooks", "status"}) })
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(output)
	}
	if got := status(); got != settings+": outdated; run: agenthail hooks install" {
		t.Fatalf("status before install = %q", got)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"hooks", "install"}) }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if hooks := gjson.GetBytes(data, "hooks.PermissionRequest.#.hooks").Array(); len(hooks) != 1 || gjson.GetBytes(data, "hooks.PermissionRequest.0.hooks.#").Int() != 1 || gjson.GetBytes(data, "hooks.PermissionRequest.0.hooks.0.command").String() != command {
		t.Fatalf("install did not update the hook in place: %s", data)
	}
	if got := status(); got != settings+": installed" {
		t.Fatalf("status after install = %q", got)
	}
}

func TestInstallMovesAgenthailsHookOutOfAMatcherThatNarrowsIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hook := func(h claudeHook) string {
		return `{"type":"command","command":` + strconv.Quote(h.command(executable)) + `,"timeout":` + strconv.FormatInt(h.timeout, 10) + `}`
	}
	if err := os.WriteFile(settings, []byte(`{"hooks":{
		"PermissionRequest":[{"matcher":"Bash","hooks":[{"type":"command","command":"guard-bash"},`+hook(claudePromptHook)+`]}],
		"SessionStart":[{"matcher":"startup","hooks":[`+hook(claudeStartHook)+`]}],
		"UserPromptSubmit":[{"hooks":[`+hook(claudePromptSubmit)+`]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	app, _ := cliFixture(t, &cliSurface{kind: "claude"})
	status := func() string {
		t.Helper()
		output, err := captureStdout(t, func() error { return app.Run([]string{"hooks", "status"}) })
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(output)
	}
	if got := status(); got != settings+": outdated; run: agenthail hooks install" {
		t.Fatalf("status before install = %q", got)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"hooks", "install"}) }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	prompt := gjson.GetBytes(data, "hooks.PermissionRequest").Array()
	if len(prompt) != 2 || prompt[0].Get("matcher").String() != "Bash" || prompt[0].Get("hooks.#").Int() != 1 || prompt[0].Get("hooks.0.command").String() != "guard-bash" || prompt[1].Get("matcher").String() != "*" || !claudePromptHook.owns(prompt[1].Get("hooks.0.command").String()) {
		t.Fatalf("PermissionRequest after install: %s", gjson.GetBytes(data, "hooks.PermissionRequest").Raw)
	}
	if start := gjson.GetBytes(data, "hooks.SessionStart").Array(); len(start) != 1 || start[0].Get("matcher").Exists() || !claudeStartHook.owns(start[0].Get("hooks.0.command").String()) {
		t.Fatalf("SessionStart after install: %s", gjson.GetBytes(data, "hooks.SessionStart").Raw)
	}
	if got := status(); got != settings+": installed" {
		t.Fatalf("status after install = %q", got)
	}
}
