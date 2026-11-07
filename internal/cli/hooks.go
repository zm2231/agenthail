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
	"time"

	"github.com/zm2231/agenthail/internal/claudehooks"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const claudeHookInputLimit = 1 << 20

// claudeSessionContextLimit bounds the session-context hook, registry locks
// included, well inside the hook's timeout: a prompt never waits on it.
var claudeSessionContextLimit = 5 * time.Second

// claudeSessionContextExpired starts the limit; tests run the hook without one.
var claudeSessionContextExpired = func() <-chan time.Time { return time.After(claudeSessionContextLimit) }

// claudePromptHookRetry is how often the hook posts its prompt again while
// no daemon answers, as while one restarts for an update.
var claudePromptHookRetry = 2 * time.Second

// claudePromptHookHeld is how long a failed post must have lasted for a daemon
// to have held the prompt; one that failed sooner was refused outright.
var claudePromptHookHeld = 2 * time.Second

// claudePromptHookUnheldLimit is how long the hook keeps posting while no
// daemon holds its prompt even though the daemon's service is loaded, as when
// the service crash-loops or runs a missing binary. A restart takes seconds.
var claudePromptHookUnheldLimit = 2 * time.Minute

func (a *App) cmdHook(args []string) error {
	a.OpenRegistry = a.OpenCurrentRegistry
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
		input := os.Stdin
		go func() { done <- a.sessionContext(input, kind, parent, time.Now()) }()
		select {
		case output := <-done:
			if output != nil {
				_ = emit(output)
			}
		case <-claudeSessionContextExpired():
		}
		return nil
	}
	if len(args) > 0 && args[0] == "stop" {
		if len(args) != 3 || args[1] != "--surface" || (args[2] != string(surface.KindClaude) && args[2] != string(surface.KindCodex)) {
			return errors.New("usage: agenthail hook stop --surface claude|codex")
		}
		done := make(chan map[string]any, 1)
		input := os.Stdin
		go func() { done <- a.turnStop(input, time.Now()) }()
		select {
		case output := <-done:
			if output != nil {
				_ = emit(output)
			}
		case <-claudeSessionContextExpired():
		}
		return nil
	}
	if len(args) != 1 || args[0] != "permission-request" {
		return errors.New("usage: agenthail hook permission-request|session-context|stop")
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
	decision := holdClaudePrompt(ctx, client, os.Stdin, parent, a.isDaemonServiceLoaded)
	if decision != nil {
		_ = emit(map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PermissionRequest", "decision": decision}})
	}
	return nil
}

// holdClaudePrompt passes the prompt to the daemon and waits for a person's
// decision; returning none leaves the prompt to Claude's own dialog. Only a
// loaded daemon service brings a daemon back, so only then is a failed
// connection posted again, until no daemon has held the prompt for
// claudePromptHookUnheldLimit. A call that failed within claudePromptHookHeld
// never counts as held, so a daemon that crashes as it starts cannot extend it.
func holdClaudePrompt(ctx context.Context, client *daemonAPIClient, input io.Reader, parent int, serviceLoaded func() bool) *surfaces.ClaudePromptDecision {
	var request daemon.ClaudePermissionRequest
	if err := json.NewDecoder(io.LimitReader(input, claudeHookInputLimit)).Decode(&request.Prompt); err != nil {
		return nil
	}
	request.PID = parent
	request.AskedAtMS = time.Now().UnixMilli()
	var body []byte
	var unheldSince time.Time
	for {
		var status int
		var err error
		asked := time.Now()
		body, status, err = client.call(ctx, http.MethodPost, "/api/v1/claude/permission-request", nil, request, nil)
		if err == nil && status == http.StatusOK {
			break
		}
		if status != 0 || ctx.Err() != nil {
			return nil
		}
		now := time.Now()
		if unheldSince.IsZero() || now.Sub(asked) > claudePromptHookHeld {
			if !serviceLoaded() {
				return nil
			}
			unheldSince = now
		}
		if now.Sub(unheldSince) >= claudePromptHookUnheldLimit {
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
	usage := errors.New("usage: agenthail hooks install [--remote-control on|off] | uninstall|status [codex]")
	var remoteControl *bool
	if hasFlag(args, "--remote-control") {
		value := flagVal(args, "--remote-control")
		if value != "on" && value != "off" {
			return usage
		}
		on := value == "on"
		remoteControl = &on
	}
	args = stripFlags(args)
	if remoteControl != nil && (len(args) != 1 || args[0] != "install") {
		return usage
	}
	if len(args) < 1 || len(args) > 2 || (args[0] != "install" && args[0] != "uninstall" && args[0] != "status") || (len(args) == 2 && args[1] != "codex") {
		return usage
	}
	if len(args) == 2 {
		return cmdCodexHooks(args[0])
	}
	if remoteControl != nil && !a.claudeRemoteControlAvailable() {
		return errors.New("this install has no Claude Remote Control, so Agenthail could not use it; leave --remote-control off this command")
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
	if err := a.claudeHooksAt(args[0], roots, executable); err != nil {
		return err
	}
	if args[0] == "uninstall" || !a.claudeRemoteControlAvailable() {
		return nil
	}
	return reportClaudeRemoteControl(home, args[0], remoteControl)
}

func (a *App) claudeRemoteControlAvailable() bool {
	source, ok := a.adapters().ForSurface(surface.KindClaude).(interface{ RemoteControlAvailable() bool })
	return ok && source.RemoteControlAvailable()
}

// reportClaudeRemoteControl sets remoteControlAtStartup in every root when
// asked, and otherwise shows it: status for each root, install only when a
// root leaves it unset.
func reportClaudeRemoteControl(home, action string, set *bool) error {
	if set != nil {
		roots, err := surface.ClaudeConfigRoots(home)
		if err != nil {
			return err
		}
		for _, root := range roots {
			changed, err := surface.WriteClaudeRemoteControl(root, *set)
			if err != nil {
				return err
			}
			fmt.Printf("%s: Remote Control at startup %s%s\n", surface.ClaudeSettingsPath(root), onOff(*set), map[bool]string{true: "", false: " (already)"}[changed])
		}
		return nil
	}
	settings, err := surface.ReadClaudeRemoteControl(home)
	if err != nil {
		return err
	}
	unset := 0
	for _, setting := range settings {
		if setting.Problem == "" && setting.AtStartup == nil {
			unset++
		}
		if action == "status" {
			fmt.Printf("%s: Remote Control at startup %s\n", setting.SettingsPath, describeRemoteControlAtStartup(setting))
		}
	}
	if action == "install" && unset > 0 {
		fmt.Println(remoteControlHint)
	}
	return nil
}

const remoteControlHint = "Remote Control at startup is unset; agenthail hooks install --remote-control on starts it in each new Claude session, so what you write in Agenthail arrives there as your own turn."

func describeRemoteControlAtStartup(setting surface.ClaudeRemoteControl) string {
	switch {
	case setting.Problem != "":
		return "unknown: " + setting.Problem
	case setting.AtStartup == nil:
		return "unset (Claude's default)"
	}
	return onOff(*setting.AtStartup)
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func (a *App) claudeHooksAt(action string, roots []string, executable string) error {
	for _, root := range roots {
		path := filepath.Join(root, "settings.json")
		switch action {
		case "install":
			changed, err := claudehooks.InstallAt(path, executable)
			if err != nil {
				return err
			}
			fmt.Printf("%s: %s\n", path, map[bool]string{true: "installed", false: "already installed"}[changed])
		case "uninstall":
			changed, err := claudehooks.UninstallAt(path)
			if err != nil {
				return err
			}
			fmt.Printf("%s: %s\n", path, map[bool]string{true: "removed", false: "not installed"}[changed])
		case "status":
			state, err := claudehooks.AllStateAt(path)
			if err != nil {
				return err
			}
			if state == claudehooks.Outdated {
				state += "; run: agenthail hooks install"
			}
			fmt.Printf("%s: %s\n", path, state)
		}
	}
	return nil
}

// cmdCodexHooks has the person's own Codex sessions told who they are. It
// needs a Codex that can run `codex app-server`, which writes its own config.
func cmdCodexHooks(action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	executable := surfaces.AgenthailExecutable()
	if executable == "" {
		return errors.New("run this from the installed agenthail binary; Codex hooks name the binary that wrote them")
	}
	switch action {
	case "install":
		changed, err := surfaces.InstallCodexContext(ctx, executable)
		if err != nil {
			return err
		}
		fmt.Printf("codex: %s\n", map[bool]string{true: "installed", false: "already installed"}[changed])
	case "uninstall":
		changed, err := surfaces.UninstallCodexContext(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("codex: %s\n", map[bool]string{true: "removed", false: "not installed"}[changed])
	case "status":
		state, err := surfaces.CodexContextState(ctx, executable)
		if err != nil {
			return err
		}
		switch state {
		case "":
			state = "Codex is not set up for this user"
		case surfaces.CodexHooksOutdated:
			state += "; run: agenthail hooks install codex"
		case surfaces.CodexHooksUntrusted:
			state = "Codex hook not trusted yet: run agenthail hooks install codex or trust it in Codex"
		}
		fmt.Printf("codex: %s\n", state)
	}
	return nil
}
