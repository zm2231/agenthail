package surface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/google/uuid"
)

type commandRunner func(context.Context, string, ...string) ([]byte, error)

func defaultCommandRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type processLauncher struct {
	id       string
	command  string
	agents   []SurfaceKind
	run      commandRunner
	pidAlive func(int) bool
	ps       commandRunner
}

func newCMUX() Launcher {
	return &processLauncher{id: LauncherCMUX, command: "cmux", agents: []SurfaceKind{KindClaude, KindCodex}, run: defaultCommandRunner, pidAlive: pidExists, ps: defaultCommandRunner}
}

func newTMUX() Launcher {
	return &processLauncher{id: LauncherTMUX, command: "tmux", agents: []SurfaceKind{KindClaude, KindCodex}, run: defaultCommandRunner, pidAlive: pidExists, ps: defaultCommandRunner}
}

func (l *processLauncher) ID() string            { return l.id }
func (l *processLauncher) Agents() []SurfaceKind { return append([]SurfaceKind(nil), l.agents...) }

func (l *processLauncher) Available(ctx context.Context) (bool, string) {
	path, err := exec.LookPath(l.command)
	if err != nil {
		return false, fmt.Sprintf("%s executable is not installed", l.command)
	}
	if l.id == LauncherCMUX {
		for _, args := range [][]string{{"new-workspace", "--help"}, {"sessions", "list", "--help"}, {"select-workspace", "--help"}, {"focus-panel", "--help"}} {
			out, err := l.run(ctx, path, args...)
			if err != nil || len(bytes.TrimSpace(out)) == 0 {
				return false, fmt.Sprintf("cmux command %q is unavailable", strings.Join(args[:len(args)-1], " "))
			}
		}
	}
	return true, path
}

func (l *processLauncher) Launch(ctx context.Context, request LaunchRequest) (LaunchResult, error) {
	if err := validateLaunchRequest(request, l.agents); err != nil {
		return LaunchResult{}, err
	}
	argv, err := agentArgv(request)
	if err != nil {
		return LaunchResult{}, err
	}
	if l.id == LauncherCMUX {
		command, intentPath, err := writeLauncherIntent(request.Cwd, argv)
		if err != nil {
			return LaunchResult{}, err
		}
		args := []string{"new-workspace", "--json", "--cwd", request.Cwd, "--command", command}
		if request.Name != "" {
			args = append(args, "--name", request.Name)
		}
		out, runErr := l.run(ctx, l.command, args...)
		if runErr != nil {
			_ = os.Remove(intentPath)
			return LaunchResult{}, fmt.Errorf("%s launch: %w: %s", l.id, runErr, strings.TrimSpace(string(out)))
		}
		result, parseErr := parseLaunchResult(l.id, out)
		if parseErr != nil {
			_ = os.Remove(intentPath)
			return LaunchResult{}, LaunchAcceptedError{Launcher: l.id, Err: parseErr}
		}
		return result, nil
	}

	sessionName := "agenthail-" + uuid.NewString()
	args := []string{"new-session", "-d", "-s", sessionName, "-c", request.Cwd, "-P", "-F", "#{session_name} #{pane_id}"}
	if request.Name != "" {
		args = append(args, "-n", request.Name)
	}
	// tmux executes multiple shell-command arguments directly; do not collapse
	// the target program and its user data into a shell command string.
	args = append(args, argv...)
	out, err := l.run(ctx, l.command, args...)
	if err != nil {
		return LaunchResult{}, fmt.Errorf("%s launch: %w: %s", l.id, err, strings.TrimSpace(string(out)))
	}
	result, parseErr := parseLaunchResult(l.id, out)
	if parseErr != nil {
		return LaunchResult{}, parseErr
	}
	return result, nil
}

func validateLaunchRequest(request LaunchRequest, agents []SurfaceKind) error {
	if !contains(agents, request.Agent) {
		return fmt.Errorf("unsupported agent %q", request.Agent)
	}
	if request.Cwd == "" || !filepath.IsAbs(request.Cwd) {
		return errors.New("absolute cwd is required")
	}
	if strings.TrimSpace(request.Message) == "" {
		return errors.New("message is required")
	}
	if strings.IndexByte(request.Message, 0) >= 0 {
		return errors.New("message contains NUL")
	}
	return nil
}

func agentArgv(request LaunchRequest) ([]string, error) {
	if request.Agent == KindClaude {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home for Claude executable: %w", err)
		}
		binary, err := ClaudeBinary(home)
		if err != nil {
			return nil, err
		}
		argv := []string{binary}
		if request.Model != "" {
			argv = append(argv, "--model", request.Model)
		}
		if request.Name != "" {
			argv = append(argv, "--name", request.Name)
		}
		return append(argv, "--", request.Message), nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve agenthail executable: %w", err)
	}
	argv := []string{executable, "codex", "--cd", request.Cwd}
	if request.Model != "" {
		argv = append(argv, "--model", request.Model)
	}
	return append(argv, "--", request.Message), nil
}

type launcherIntent struct {
	Argv []string `json:"argv"`
	Cwd  string   `json:"cwd"`
}

func writeLauncherIntent(cwd string, argv []string) (string, string, error) {
	payload, err := json.Marshal(launcherIntent{Argv: argv, Cwd: cwd})
	if err != nil {
		return "", "", fmt.Errorf("encode launcher intent: %w", err)
	}
	file, err := os.CreateTemp("", "agenthail-launch-*.json")
	if err != nil {
		return "", "", fmt.Errorf("create launcher intent: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", "", fmt.Errorf("protect launcher intent: %w", err)
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", "", fmt.Errorf("write launcher intent: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", "", fmt.Errorf("close launcher intent: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		_ = os.Remove(path)
		return "", "", fmt.Errorf("resolve launcher executable: %w", err)
	}
	command := "exec " + shellQuote(executable) + " launcher-exec " + shellQuote(path)
	return command, path, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func parseLaunchResult(id string, out []byte) (LaunchResult, error) {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return LaunchResult{}, nil
	}
	if id == LauncherTMUX {
		fields := strings.Fields(text)
		location := &Location{}
		if len(fields) > 0 {
			location.Session = fields[0]
		}
		if len(fields) > 1 {
			location.Pane = fields[1]
		}
		return LaunchResult{Location: location}, nil
	}
	var payload struct {
		WorkspaceRef string `json:"workspace_ref"`
		WorkspaceID  string `json:"workspace_id"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return LaunchResult{}, fmt.Errorf("parse %s launch result: %w", id, err)
	}
	workspace := payload.WorkspaceRef
	if workspace == "" {
		workspace = payload.WorkspaceID
	}
	if workspace == "" {
		return LaunchResult{}, fmt.Errorf("parse %s launch result: workspace handle is missing", id)
	}
	return LaunchResult{Location: &Location{Workspace: workspace}}, nil
}

func (l *processLauncher) Locate(ctx context.Context, sessions []Session) map[string]Location {
	if l.id == LauncherCMUX {
		return l.locateCMUX(ctx, sessions)
	}
	return l.locateTMUX(ctx, sessions)
}

type cmuxRecord struct {
	SessionID       string `json:"session_id"`
	Workspace       string `json:"workspace_id"`
	Surface         string `json:"surface_id"`
	PID             int    `json:"pid"`
	StoredPIDExists *bool  `json:"stored_pid_exists"`
}

func (l *processLauncher) locateCMUX(ctx context.Context, sessions []Session) map[string]Location {
	out, err := l.run(ctx, l.command, "sessions", "list", "--json", "--all")
	if err != nil {
		return nil
	}
	var envelope struct {
		Sessions []cmuxRecord `json:"sessions"`
	}
	if json.Unmarshal(out, &envelope) != nil {
		return nil
	}
	wanted := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		wanted[session.ID] = struct{}{}
	}
	located := make(map[string]Location)
	for _, record := range envelope.Sessions {
		if record.SessionID == "" || record.PID <= 0 || (record.StoredPIDExists != nil && !*record.StoredPIDExists) || !l.pidAlive(record.PID) {
			continue
		}
		if _, ok := wanted[record.SessionID]; !ok {
			continue
		}
		located[record.SessionID] = Location{Workspace: record.Workspace, Surface: record.Surface}
	}
	return located
}

type tmuxPane struct {
	PID     int
	Session string
	Pane    string
	Cwd     string
}

func (l *processLauncher) locateTMUX(ctx context.Context, sessions []Session) map[string]Location {
	out, err := l.run(ctx, l.command, "list-panes", "-a", "-F", "#{pane_pid} #{session_name} #{pane_id} #{pane_current_path}")
	if err != nil {
		return nil
	}
	var panes []tmuxPane
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.SplitN(line, " ", 4)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || pid <= 0 {
			continue
		}
		panes = append(panes, tmuxPane{PID: pid, Session: fields[1], Pane: fields[2], Cwd: fields[3]})
	}
	located := make(map[string]Location)
	for _, session := range sessions {
		if session.PID <= 0 || !l.pidAlive(session.PID) {
			continue
		}
		for _, pane := range panes {
			if filepath.Clean(session.Cwd) != filepath.Clean(pane.Cwd) || !l.pidAlive(pane.PID) {
				continue
			}
			if !l.processDescendsFrom(ctx, session.PID, pane.PID) {
				continue
			}
			located[session.ID] = Location{Session: pane.Session, Pane: pane.Pane}
			break
		}
	}
	return located
}

func (l *processLauncher) processDescendsFrom(ctx context.Context, child, ancestor int) bool {
	seen := map[int]bool{}
	for child > 1 && !seen[child] {
		if child == ancestor {
			return true
		}
		seen[child] = true
		out, err := l.ps(ctx, "ps", "-o", "ppid=", "-p", strconv.Itoa(child))
		if err != nil {
			return false
		}
		fields := strings.Fields(string(out))
		if len(fields) != 1 {
			return false
		}
		parent, err := strconv.Atoi(fields[0])
		if err != nil {
			return false
		}
		child = parent
	}
	return false
}

func pidExists(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

func (l *processLauncher) Focus(ctx context.Context, location Location) error {
	if l.id == LauncherCMUX {
		if location.Workspace == "" || location.Surface == "" {
			return errors.New("cmux focus requires workspace and surface")
		}
		out, err := l.run(ctx, l.command, "select-workspace", "--workspace", location.Workspace)
		if err != nil {
			return fmt.Errorf("select cmux workspace: %w: %s", err, strings.TrimSpace(string(out)))
		}
		out, err = l.run(ctx, l.command, "focus-panel", "--panel", location.Surface)
		if err != nil {
			return fmt.Errorf("focus cmux surface: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if location.Session == "" {
		return errors.New("tmux focus requires a session")
	}
	clients, err := l.run(ctx, l.command, "list-clients", "-F", "#{client_name} #{session_name}")
	if err != nil {
		return fmt.Errorf("list tmux clients: %w: %s", err, strings.TrimSpace(string(clients)))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(clients)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != location.Session {
			continue
		}
		if location.Pane != "" {
			if out, err := l.run(ctx, l.command, "select-pane", "-t", location.Pane); err != nil {
				return fmt.Errorf("select tmux pane: %w: %s", err, strings.TrimSpace(string(out)))
			}
		}
		if out, err := l.run(ctx, l.command, "switch-client", "-c", fields[0], "-t", location.Session); err != nil {
			return fmt.Errorf("switch tmux client: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	attach := "tmux attach-session -t " + shellQuote(location.Session)
	script := `tell application "Terminal" to do script ` + appleScriptQuote(attach)
	if out, err := l.run(ctx, "osascript", "-e", script); err != nil {
		return fmt.Errorf("attach Terminal to tmux session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func appleScriptQuote(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}
