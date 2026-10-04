package surface

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

type launcherStarter struct{ options *SessionStartOptions }

func (s *launcherStarter) StartSession(_ context.Context, options SessionStartOptions) (*Session, *SendResult, error) {
	s.options = &options
	return &Session{ID: "created"}, nil, nil
}

func (launcherStarter) Name() SurfaceKind                                           { return KindClaude }
func (launcherStarter) List(context.Context) ([]Session, error)                     { return nil, nil }
func (launcherStarter) Resolve(context.Context, string) (*Session, error)           { return nil, nil }
func (launcherStarter) Observe(context.Context, *Session) (*TurnObservation, error) { return nil, nil }
func (launcherStarter) Send(context.Context, *Session, string) (*SendResult, error) { return nil, nil }
func (launcherStarter) Reply(context.Context, *Session, int) (*ReplyResult, error)  { return nil, nil }
func (launcherStarter) Tail(context.Context, *Session, int) ([]Exchange, error)     { return nil, nil }
func (launcherStarter) Stream(context.Context, *Session, string, func(StreamEvent), time.Duration) error {
	return nil
}
func (launcherStarter) GoalSet(context.Context, *Session, string) error         { return nil }
func (launcherStarter) GoalClear(context.Context, *Session) error               { return nil }
func (launcherStarter) GoalGet(context.Context, *Session) (*GoalState, error)   { return nil, nil }
func (launcherStarter) Compact(context.Context, *Session) error                 { return nil }
func (launcherStarter) Model(context.Context, *Session, string) (string, error) { return "", nil }
func (launcherStarter) Interrupt(context.Context, *Session) error               { return nil }
func (launcherStarter) Steer(context.Context, *Session, string) error           { return nil }
func (launcherStarter) Capabilities() Capabilities                              { return Capabilities{} }

func TestLauncherRuntimeJSONOmitsLocationOnlyWhenUnset(t *testing.T) {
	payload, err := json.Marshal(Session{ID: "s", Runtime: &Runtime{Launcher: LauncherCMUX, Focusable: true, Location: &Location{Workspace: "w"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(payload); !strings.Contains(got, `"runtime":{"launcher":"cmux","location":{"workspace":"w"},"focusable":true}`) {
		t.Fatalf("runtime JSON = %s", got)
	}
	legacy, err := json.Marshal(Session{ID: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "runtime") {
		t.Fatalf("legacy session unexpectedly has runtime: %s", legacy)
	}
}

func TestNewLaunchersInjectsExistingStarterAndSeams(t *testing.T) {
	starter := &launcherStarter{}
	launchers := NewLaunchers([]Surface{starter})
	ids := make([]string, 0, len(launchers))
	for _, launcher := range launchers {
		ids = append(ids, launcher.ID())
	}
	want := []string{LauncherCMUX, LauncherTMUX, LauncherClaudeBG, LauncherClaudeSDK, LauncherExternal}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("launcher ids = %#v, want %#v", ids, want)
	}
	result, err := launchers[2].Launch(context.Background(), LaunchRequest{Agent: KindClaude, Message: "hello"})
	if err != nil || result.SessionID != "created" {
		t.Fatalf("starter launch = %#v, %v", result, err)
	}
	if starter.options == nil || starter.options.Agent != "" || starter.options.Owner != "" {
		t.Fatalf("starter options unexpectedly selected an agent/owner: %#v", starter.options)
	}
}

func TestCMUXLaunchUsesDirectArgvAndKeepsMessageOneArgument(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args")
	writeExecutable(t, filepath.Join(dir, "cmux"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CMUX_TEST_ARGS\"\nprintf '%s' 'workspace:1'\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CMUX_TEST_ARGS", logPath)
	launcher := newCMUX().(*processLauncher)
	request := LaunchRequest{Agent: KindCodex, Cwd: "/tmp/project", Model: "model x", Message: `$(touch /tmp/nope); "quoted"`}
	result, err := launcher.Launch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Location == nil || result.Location.Workspace != "workspace:1" {
		t.Fatalf("result = %#v", result)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n")
	if !containsLine(lines, "--command") || containsLine(lines, request.Message) {
		t.Fatalf("argv log = %#v", lines)
	}
	command := lines[len(lines)-1]
	match := regexp.MustCompile(`launcher-exec '([^']+)'`).FindStringSubmatch(command)
	if len(match) != 2 {
		t.Fatalf("unsafe cmux command = %q", command)
	}
	data, err := os.ReadFile(match[1])
	if err != nil {
		t.Fatal(err)
	}
	var intent launcherIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		t.Fatal(err)
	}
	if len(intent.Argv) == 0 || intent.Argv[len(intent.Argv)-1] != request.Message {
		t.Fatalf("intent did not preserve message: %#v", intent)
	}
	if info, err := os.Stat(match[1]); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("intent permissions = %v, %v", info, err)
	}
	_ = os.Remove(match[1])
}

func TestCMUXLocateRequiresLivePID(t *testing.T) {
	launcher := newCMUX().(*processLauncher)
	launcher.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"sessions":[{"session_id":"live","workspace_id":"w","surface_id":"s","pane_id":"p","pid":7},{"session_id":"dead","pid":8}]}`), nil
	}
	launcher.pidAlive = func(pid int) bool { return pid == 7 }
	got := launcher.Locate(context.Background(), []Session{{ID: "live"}, {ID: "dead"}})
	want := map[string]Location{"live": {Workspace: "w", Surface: "s", Pane: "p", Session: "live"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locations = %#v, want %#v", got, want)
	}
}

func TestCMUXAvailabilityProbesCommandsInsteadOfHelpText(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "cmux"), "#!/bin/sh\ncase \"$1 $2\" in\n  'new-workspace --help'|'sessions list --help'|'select-workspace --help'|'focus-panel --help') printf 'supported\\n'; exit 0;;\n  *) printf 'run is only a word here\\n'; exit 0;;\nesac\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	launcher := newCMUX().(*processLauncher)
	launcher.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		return defaultCommandRunner(context.Background(), name, args...)
	}
	available, detail := launcher.Available(context.Background())
	if !available || detail != filepath.Join(dir, "cmux") {
		t.Fatalf("availability = %v, %q", available, detail)
	}
}

func TestTMUXLaunchPassesMessageAsDirectArgument(t *testing.T) {
	launcher := newTMUX().(*processLauncher)
	var got []string
	launcher.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return []byte("build:1"), nil
	}
	message := `$(touch /tmp/nope); echo unsafe`
	result, err := launcher.Launch(context.Background(), LaunchRequest{Agent: KindClaude, Cwd: "/work", Name: "build", Message: message})
	if err != nil {
		t.Fatal(err)
	}
	if result.Location == nil || result.Location.Session != "build:1" {
		t.Fatalf("result = %#v", result)
	}
	if !containsLine(got, message) {
		t.Fatalf("message was not a distinct argv element: %#v", got)
	}
	if containsLine(got, "--command") {
		t.Fatalf("tmux launch used shell command mode: %#v", got)
	}
}

func TestTMUXLocateRequiresCwdAndVerifiedAncestry(t *testing.T) {
	launcher := newTMUX().(*processLauncher)
	launcher.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("42 build %1 /work/project\n43 other %2 /work/project\n"), nil
	}
	launcher.pidAlive = func(pid int) bool { return pid == 42 || pid == 43 || pid == 99 || pid == 100 }
	launcher.ps = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 4 && args[3] == "99" {
			return []byte("42\n"), nil
		}
		return []byte("1\n"), nil
	}
	got := launcher.Locate(context.Background(), []Session{{ID: "codex", Surface: KindCodex, Cwd: "/work/project", PID: 99}, {ID: "wrong", Surface: KindClaude, Cwd: "/work/project", PID: 100}})
	want := map[string]Location{"codex": {Session: "build", Pane: "%1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locations = %#v, want %#v", got, want)
	}
}

func TestTMUXLocatePreservesCwdSpaces(t *testing.T) {
	launcher := newTMUX().(*processLauncher)
	launcher.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("42 build %1 /work/project  with  spaces\n"), nil
	}
	launcher.pidAlive = func(pid int) bool { return pid == 42 || pid == 99 }
	launcher.ps = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("42\n"), nil
	}
	got := launcher.Locate(context.Background(), []Session{{ID: "codex", Cwd: "/work/project  with  spaces", PID: 99}})
	want := map[string]Location{"codex": {Session: "build", Pane: "%1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locations = %#v, want %#v", got, want)
	}
}

func TestTMUXFocusFallsBackToTerminalAttach(t *testing.T) {
	launcher := newTMUX().(*processLauncher)
	var calls [][]string
	launcher.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if name == "tmux" {
			return nil, os.ErrNotExist
		}
		return nil, nil
	}
	if err := launcher.Focus(context.Background(), Location{Session: "build"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0][0] != "tmux" || calls[1][0] != "osascript" {
		t.Fatalf("focus calls = %#v", calls)
	}
	if len(calls[1]) < 3 || !strings.Contains(calls[1][2], "build") {
		t.Fatalf("attach call = %#v", calls[1])
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
}

func containsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}
