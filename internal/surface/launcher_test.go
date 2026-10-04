package surface

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

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

func TestCMUXLaunchUsesDirectArgvAndKeepsMessageOneArgument(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args")
	writeExecutable(t, filepath.Join(dir, "cmux"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CMUX_TEST_ARGS\"\nprintf '%s' '{\"workspace_ref\":\"workspace:1\"}'\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CMUX_TEST_ARGS", logPath)
	launcher := newCMUX().(*processLauncher)
	request := LaunchRequest{Agent: KindCodex, Cwd: "/tmp/project", Model: "model x", Name: "named", Message: `$(touch /tmp/nope); "quoted"`}
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
	if !containsLine(lines, "--json") || !containsLine(lines, "--command") || !containsLine(lines, request.Name) || containsLine(lines, request.Message) {
		t.Fatalf("argv log = %#v", lines)
	}
	commandIndex := slices.Index(lines, "--command")
	if commandIndex < 0 || commandIndex+1 >= len(lines) {
		t.Fatalf("cmux command argument missing: %#v", lines)
	}
	command := lines[commandIndex+1]
	if !strings.Contains(command, "AGENTHAIL_CODEX_LAUNCH_RUNTIME=cmux") {
		t.Fatalf("cmux launch omitted runtime discriminator: %q", command)
	}
	match := regexp.MustCompile(`launcher-exec '([^']+)'`).FindStringSubmatch(command)
	if len(match) != 2 {
		t.Fatalf("unsafe cmux command = %q", command)
	}
	data, err := os.ReadFile(match[1])
	if err != nil {
		t.Fatal(err)
	}
	var intent struct {
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal(data, &intent); err != nil {
		t.Fatal(err)
	}
	if len(intent.Argv) == 0 || intent.Argv[len(intent.Argv)-1] != request.Message {
		t.Fatalf("intent did not preserve message: %#v", intent)
	}
	if executable, err := os.Executable(); err != nil || intent.Argv[0] != executable {
		t.Fatalf("intent argv[0]=%q, want agenthail executable %q (%v)", intent.Argv[0], executable, err)
	}
	if info, err := os.Stat(match[1]); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("intent permissions = %v, %v", info, err)
	}
	_ = os.Remove(match[1])
}

func TestCMUXMalformedAcceptedOutputCleansIntentAndReturnsAcceptedError(t *testing.T) {
	claude := filepath.Join(t.TempDir(), "claude")
	writeExecutable(t, claude, "#!/bin/sh\nexit 0\n")
	t.Setenv("AGENTHAIL_CLAUDE_BIN", claude)
	launcher := newCMUX().(*processLauncher)
	var args []string
	launcher.run = func(_ context.Context, _ string, got ...string) ([]byte, error) {
		args = append([]string(nil), got...)
		return []byte("not-json"), nil
	}
	_, err := launcher.Launch(context.Background(), LaunchRequest{Agent: KindClaude, Cwd: "/tmp/project", Message: "hello"})
	var acceptedErr LaunchAcceptedError
	if !errors.As(err, &acceptedErr) || acceptedErr.Launcher != LauncherCMUX {
		t.Fatalf("err=%v", err)
	}
	commandIndex := slices.Index(args, "--command")
	if commandIndex < 0 || commandIndex+1 >= len(args) {
		t.Fatalf("cmux command argument missing: %#v", args)
	}
	match := regexp.MustCompile(`launcher-exec '([^']+)'`).FindStringSubmatch(args[commandIndex+1])
	if len(match) != 2 {
		t.Fatalf("cmux command = %q", args[commandIndex+1])
	}
	if _, statErr := os.Stat(match[1]); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("intent remains after accepted parse failure: %v", statErr)
	}
}

func TestCMUXLocateRequiresLivePID(t *testing.T) {
	launcher := newCMUX().(*processLauncher)
	launcher.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"sessions":[{"session_id":"live","workspace_id":"w","surface_id":"s","pid":7,"stored_pid_exists":true},{"session_id":"dead","pid":8,"stored_pid_exists":false}]}`), nil
	}
	launcher.pidAlive = func(pid int) bool { return pid == 7 }
	got := launcher.Locate(context.Background(), []Session{{ID: "live"}, {ID: "dead"}})
	want := map[string]Location{"live": {Workspace: "w", Surface: "s"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locations = %#v, want %#v", got, want)
	}
}

func TestCMUXLocateUsesAgenthailReceiptAndLiveInventory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	launcher := newCMUX().(*processLauncher)
	launcher.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if slices.Contains(args, "sessions") {
			return []byte(`{"sessions":[]}`), nil
		}
		if slices.Contains(args, "tree") {
			if !reflect.DeepEqual(args, []string{"--json", "--id-format", "both", "tree", "--workspace", "workspace-7"}) {
				t.Fatalf("tree args=%#v", args)
			}
			return []byte(`{"windows":[{"ref":"window:1","workspaces":[{"id":"workspace-7","ref":"workspace:7","panes":[{"ref":"pane:1","surfaces":[{"id":"surface-9","ref":"surface:9","type":"terminal"}]}]}]}]}`), nil
		}
		t.Fatalf("unexpected cmux command: %#v", args)
		return nil, nil
	}
	path, err := ManagedCodexLaunchReceiptPath("agenthail-launch")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteManagedCodexLaunchReceipt(path, ManagedCodexLaunchReceipt{LaunchID: "agenthail-launch", ThreadID: "codex-thread", Cwd: "/work/project", Runtime: LauncherCMUX, Workspace: "workspace-7", Surface: "surface-9"}); err != nil {
		t.Fatal(err)
	}
	got := launcher.Locate(context.Background(), []Session{{ID: "codex-thread", Surface: KindCodex, Cwd: "/work/project"}})
	want := map[string]Location{"codex-thread": {Workspace: "workspace-7", Surface: "surface-9"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locations=%#v want=%#v", got, want)
	}
}

func TestCMUXLocatePreservesReceiptOnUnverifiableInventory(t *testing.T) {
	for _, inventory := range []string{
		`{}`,
		`{"windows":[]}`,
		`{"windows":[{}]}`,
		`{"windows":[{"workspaces":[{"id":"workspace:7"}]}]}`,
		`{"windows":[{"workspaces":[{"id":"workspace:7","panes":[{}]}]}]}`,
		`{"windows":[{"workspaces":{}}]}`,
	} {
		t.Run(inventory, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			launcher := newCMUX().(*processLauncher)
			launcher.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if slices.Contains(args, "sessions") {
					return []byte(`{"sessions":[]}`), nil
				}
				return []byte(inventory), nil
			}
			path, err := ManagedCodexLaunchReceiptPath("agenthail-degraded")
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteManagedCodexLaunchReceipt(path, ManagedCodexLaunchReceipt{LaunchID: "agenthail-degraded", ThreadID: "codex-thread", Cwd: "/work/project", Runtime: LauncherCMUX, Workspace: "workspace:7", Surface: "surface:9"}); err != nil {
				t.Fatal(err)
			}
			if got := launcher.Locate(context.Background(), []Session{{ID: "codex-thread", Surface: KindCodex, Cwd: "/work/project"}}); len(got) != 0 {
				t.Fatalf("unverifiable inventory located the session: %#v", got)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("receipt was deleted after unverifiable inventory: %v", err)
			}
		})
	}
}

func TestTMUXLaunchPassesMessageAsDirectArgument(t *testing.T) {
	claude := filepath.Join(t.TempDir(), "claude")
	writeExecutable(t, claude, "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("AGENTHAIL_CLAUDE_BIN", claude)
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
	if !containsLine(got, claude) || containsLine(got, "claude") {
		t.Fatalf("tmux launch did not use the resolved Claude executable: %#v", got)
	}
}

func TestTMUXLaunchClassifiesPostDispatchFailureAsAccepted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	claude := filepath.Join(t.TempDir(), "claude")
	writeExecutable(t, claude, "#!/bin/sh\nexit 0\n")
	t.Setenv("AGENTHAIL_CLAUDE_BIN", claude)
	launchSession := func(args []string) string {
		if index := slices.Index(args, "-s"); index >= 0 && index+1 < len(args) {
			return args[index+1]
		}
		t.Fatalf("tmux session argument missing: %#v", args)
		return ""
	}
	for _, test := range []struct {
		name     string
		agent    SurfaceKind
		run      func(args []string) ([]byte, error)
		accepted bool
		identity bool
	}{
		{
			name:  "unparseable output after launch",
			agent: KindCodex,
			run: func([]string) ([]byte, error) {
				return []byte("created-but-unparseable"), nil
			},
			accepted: true,
		},
		{
			name:  "runner error after dispatch",
			agent: KindClaude,
			run: func(args []string) ([]byte, error) {
				return []byte(launchSession(args) + " %1"), errors.New("tmux exited after creating the session")
			},
			accepted: true,
			identity: true,
		},
		{
			name:  "tmux executable missing",
			agent: KindClaude,
			run: func([]string) ([]byte, error) {
				return nil, &exec.Error{Name: "tmux", Err: os.ErrNotExist}
			},
		},
		{
			name:  "tmux fork failure",
			agent: KindClaude,
			run: func([]string) ([]byte, error) {
				return nil, &os.PathError{Op: "fork/exec", Path: "/missing/tmux", Err: os.ErrNotExist}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			launcher := newTMUX().(*processLauncher)
			launcher.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				return test.run(args)
			}
			result, err := launcher.Launch(context.Background(), LaunchRequest{Agent: test.agent, Cwd: "/work", Message: "hello"})
			var acceptedErr LaunchAcceptedError
			if !test.accepted {
				if err == nil || errors.As(err, &acceptedErr) || result.Location != nil {
					t.Fatalf("result=%#v err=%v, want definitive pre-start failure", result, err)
				}
				return
			}
			if !errors.As(err, &acceptedErr) || acceptedErr.Launcher != LauncherTMUX {
				t.Fatalf("err=%v, want accepted tmux launch error", err)
			}
			if test.identity && (result.Location == nil || result.Location.Session == "") {
				t.Fatalf("result=%#v, want launch-owned session identity", result)
			}
		})
	}
}

func TestProcessLaunchFailsBeforeLaunchWhenClaudeIsMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("AGENTHAIL_CLAUDE_BIN", "")
	launcher := newTMUX().(*processLauncher)
	launched := false
	launcher.run = func(context.Context, string, ...string) ([]byte, error) {
		launched = true
		return []byte("build:1"), nil
	}
	if _, err := launcher.Launch(context.Background(), LaunchRequest{Agent: KindClaude, Cwd: "/work", Message: "hello"}); err == nil || launched {
		t.Fatalf("launch with missing Claude executable err=%v launched=%v", err, launched)
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

func TestTMUXLocateWaitsForCompleteReceiptAndRejectsTokenOrPaneMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	launcher := newTMUX().(*processLauncher)
	launcher.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("4242 agenthail-review %1 /work/project\n4343 agenthail-review %2 /work/project\n"), nil
	}
	launcher.pidAlive = func(pid int) bool { return pid == 4242 || pid == 4343 }
	sessions := []Session{{ID: "codex-thread", Surface: KindCodex, Cwd: "/work/project", PID: 0}}
	if got := launcher.Locate(context.Background(), sessions); len(got) != 0 {
		t.Fatalf("late receipt unexpectedly correlated: %#v", got)
	}
	path, err := ManagedCodexLaunchReceiptPath("agenthail-review")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"launchId":"other","threadId":"codex-thread","cwd":"/work/project","tmuxSession":"agenthail-review","tmuxPane":"%1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := launcher.Locate(context.Background(), sessions); len(got) != 0 {
		t.Fatalf("token-mismatched receipt correlated: %#v", got)
	}
	if err := WriteManagedCodexLaunchReceipt(path, ManagedCodexLaunchReceipt{LaunchID: "agenthail-review", ThreadID: "codex-thread", Cwd: "/work/project", Runtime: LauncherTMUX, TmuxSession: "agenthail-review", TmuxPane: "%2"}); err != nil {
		t.Fatal(err)
	}
	got := launcher.Locate(context.Background(), sessions)
	want := map[string]Location{"codex-thread": {Session: "agenthail-review", Pane: "%2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locations=%#v want=%#v", got, want)
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
