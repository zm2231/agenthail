package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codexconfig"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type cliSurface struct {
	kind          surface.SurfaceKind
	sessions      map[string]surface.Session
	listed        []surface.Session
	listErr       error
	listCalls     int
	caps          surface.Capabilities
	observation   *surface.TurnObservation
	observations  []*surface.TurnObservation
	observeErr    error
	sendResult    *surface.SendResult
	reply         *surface.ReplyResult
	replyWait     bool
	sendWait      bool
	tailBlock     <-chan struct{}
	sent          []string
	tail          []surface.Exchange
	streamEvents  []surface.StreamEvent
	searchResults []surface.SessionSearchResult
	searchErr     error
	compactCalls  int
}

type runtimeCLISurface struct {
	*cliSurface
	status surface.RuntimeStatus
}

type cliReadSurface struct {
	*cliSurface
	result   *surface.SessionReadResult
	err      error
	requests []surface.SessionReadRequest
}

func (f *cliReadSurface) ReadSession(_ context.Context, _ *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	f.requests = append(f.requests, request)
	return f.result, f.err
}

func TestCodexRemotePortRequiresExplicitOverride(t *testing.T) {
	t.Setenv("AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT", "")
	if got := codexconfig.RemoteDebuggingPort(); got != "" {
		t.Fatalf("RemoteDebuggingPort() = %q, want empty managed-runtime default", got)
	}
	t.Setenv("AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT", "9333")
	if got := codexconfig.RemoteDebuggingPort(); got != "9333" {
		t.Fatalf("RemoteDebuggingPort() = %q, want renderer override", got)
	}
}

func TestHelpExplainsBusyTargetContinuations(t *testing.T) {
	output, err := captureStdout(t, func() error {
		(&App{}).usage()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"queue when busy", "--no-queue", "use send when idle", "start daemon to deliver queued work"} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("help missing %q: %s", fragment, output)
		}
	}
}

func (f *runtimeCLISurface) RuntimeStatus(context.Context) surface.RuntimeStatus { return f.status }

func TestCodexCommandRejectsCustomRemote(t *testing.T) {
	err := (&App{}).Run([]string{"codex", "--remote", "ws://example.test"})
	if err == nil || !strings.Contains(err.Error(), "manages the remote transport") {
		t.Fatalf("err=%v", err)
	}
}

func TestRepairManagedCodexRuntimeRestartsConfiguredRuntime(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "args")
	script := filepath.Join(root, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$AGENTHAIL_TEST_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	t.Setenv("AGENTHAIL_TEST_LOG", logPath)
	if err := repairManagedCodexRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(logPath)
	if err != nil || strings.TrimSpace(string(output)) != "app-server daemon restart" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestRepairManagedCodexRuntimeUsesStandaloneRuntimeOverPath(t *testing.T) {
	root := t.TempDir()
	standalone := filepath.Join(root, "packages", "standalone", "current")
	if err := os.MkdirAll(standalone, 0700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "args")
	standaloneBinary := filepath.Join(standalone, "codex")
	if err := os.WriteFile(standaloneBinary, []byte("#!/bin/sh\nprintf 'standalone:%s\\n' \"$*\" > \"$AGENTHAIL_TEST_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	pathDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(pathDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pathDir, "codex"), []byte("#!/bin/sh\nprintf 'path:%s\\n' \"$*\" > \"$AGENTHAIL_TEST_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", root)
	t.Setenv("AGENTHAIL_CODEX_BIN", "")
	t.Setenv("AGENTHAIL_TEST_LOG", logPath)
	t.Setenv("PATH", pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := repairManagedCodexRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(logPath)
	if err != nil || strings.TrimSpace(string(output)) != "standalone:app-server daemon restart" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

type readinessCLISurface struct {
	*cliSurface
	readyCalls int
	readyErr   error
}

func (f *readinessCLISurface) Ready(context.Context) error {
	f.readyCalls++
	return f.readyErr
}

func TestProbeCodexAppServerUsesReadinessCheck(t *testing.T) {
	fake := &cliSurface{listErr: errors.New("thread/list unavailable")}
	ready := &readinessCLISurface{cliSurface: fake, readyErr: errors.New("thread/loaded/list unavailable")}
	err := probeCodexAppServer(ready)
	if !errors.Is(err, ready.readyErr) || ready.readyCalls != 1 || fake.listCalls != 0 {
		t.Fatalf("err=%v ready_calls=%d list_calls=%d", err, ready.readyCalls, fake.listCalls)
	}
}

func TestProbeCodexAppServerFallsBackToList(t *testing.T) {
	fake := &cliSurface{listErr: errors.New("thread/list unavailable")}
	err := probeCodexAppServer(fake)
	if !errors.Is(err, fake.listErr) || fake.listCalls != 1 {
		t.Fatalf("err=%v calls=%d", err, fake.listCalls)
	}
}

func TestCodexLaunchProbeTimeoutBoundsReadiness(t *testing.T) {
	if codexLaunchProbeTimeout != 5*time.Second {
		t.Fatalf("codexLaunchProbeTimeout = %s, want 5s", codexLaunchProbeTimeout)
	}
}

func TestCodexRemoteArgsUseCallerDirectory(t *testing.T) {
	got := codexRemoteArgs([]string{"--model", "gpt-5.6-sol"}, "/tmp/project")
	want := []string{"--cd", "/tmp/project", "--model", "gpt-5.6-sol"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func TestCodexRemoteArgsPreserveExplicitDirectory(t *testing.T) {
	for _, args := range [][]string{{"-C", "/tmp/other"}, {"-C/tmp/other"}, {"--cd", "/tmp/other"}, {"--cd=/tmp/other"}} {
		got := codexRemoteArgs(args, "/tmp/project")
		if strings.Join(got, "\x00") != strings.Join(args, "\x00") {
			t.Fatalf("args=%q got=%q", args, got)
		}
	}
}

func TestCodexRemoteArgsIgnoreArgumentsAfterTerminator(t *testing.T) {
	got := codexRemoteArgs([]string{"--", "-C", "literal"}, "/tmp/project")
	want := []string{"--cd", "/tmp/project", "--", "-C", "literal"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("got=%q want=%q", got, want)
	}
}

func (f *cliSurface) Name() surface.SurfaceKind { return f.kind }
func (f *cliSurface) List(context.Context) ([]surface.Session, error) {
	f.listCalls++
	return f.listed, f.listErr
}
func (f *cliSurface) Resolve(_ context.Context, target string) (*surface.Session, error) {
	session, ok := f.sessions[target]
	if !ok {
		return nil, errors.New("not found")
	}
	return &session, nil
}
func (f *cliSurface) SearchSessions(context.Context, string, int) ([]surface.SessionSearchResult, error) {
	return f.searchResults, f.searchErr
}
func (f *cliSurface) Observe(context.Context, *surface.Session) (*surface.TurnObservation, error) {
	if len(f.observations) > 0 {
		observation := f.observations[0]
		f.observations = f.observations[1:]
		return observation, f.observeErr
	}
	return f.observation, f.observeErr
}
func (f *cliSurface) Send(ctx context.Context, _ *surface.Session, message string) (*surface.SendResult, error) {
	f.sent = append(f.sent, message)
	if f.sendWait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.sendResult == nil {
		return &surface.SendResult{UUID: "turn", Accepted: true}, nil
	}
	return f.sendResult, nil
}
func (f *cliSurface) Reply(ctx context.Context, _ *surface.Session, _ int) (*surface.ReplyResult, error) {
	if f.replyWait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.reply != nil {
		return f.reply, nil
	}
	return &surface.ReplyResult{Done: true}, nil
}

func (f *cliSurface) Tail(_ context.Context, _ *surface.Session, _ int) ([]surface.Exchange, error) {
	if f.tailBlock != nil {
		<-f.tailBlock
	}
	return f.tail, nil
}
func (f *cliSurface) Stream(_ context.Context, _ *surface.Session, _ string, callback func(surface.StreamEvent), _ time.Duration) error {
	for _, event := range f.streamEvents {
		callback(event)
	}
	return nil
}
func (*cliSurface) GoalSet(context.Context, *surface.Session, string) error { return nil }
func (*cliSurface) GoalClear(context.Context, *surface.Session) error       { return nil }
func (*cliSurface) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	return &surface.GoalState{Objective: "ship", Status: "active"}, nil
}
func (f *cliSurface) Compact(context.Context, *surface.Session) error {
	f.compactCalls++
	return nil
}
func (*cliSurface) Model(context.Context, *surface.Session, string) (string, error) {
	return "model", nil
}
func (*cliSurface) Interrupt(context.Context, *surface.Session) error     { return nil }
func (*cliSurface) Steer(context.Context, *surface.Session, string) error { return nil }
func (f *cliSurface) Capabilities() surface.Capabilities                  { return f.caps }
func (*cliSurface) EnsureWritable(_ context.Context, session *surface.Session) error {
	if session.Source == "cli" || session.Transport == "readOnly" {
		return errors.New(surface.ReadOnlySessionReason(session))
	}
	session.Transport = "managed"
	return nil
}

func cliFixture(t *testing.T, fake *cliSurface) (*App, *registry.Registry) {
	t.Helper()
	for _, key := range []string{"AGENTHAIL_SESSION_ID", "CODEX_THREAD_ID", "CLAUDE_SESSION_ID"} {
		t.Setenv(key, "")
	}
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return &App{Registry: r, Surfaces: []SurfaceEntry{{Name: string(fake.kind), Surface: fake}}, DefaultTimeout: time.Second}, r
}

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	runErr := run()
	writer.Close()
	os.Stdout = old
	data, readErr := io.ReadAll(reader)
	reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data), runErr
}

func TestValidateCommandFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantError string
	}{
		{"unknown", []string{"--bogus"}, "unknown flag"},
		{"missing value", []string{"--model", "--json"}, "requires a value"},
		{"duplicate", []string{"--reply", "--reply"}, "only be specified once"},
		{"no queue", []string{"target", "message", "--no-queue", "--json"}, ""},
		{"terminator", []string{"target", "--", "--literal"}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCommandFlags("send", test.args)
			if test.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCommandTimeout(t *testing.T) {
	got, err := commandTimeout([]string{"--timeout", "3m"}, time.Second)
	if err != nil || got != 3*time.Minute {
		t.Fatalf("got=%s err=%v", got, err)
	}
	if _, err := commandTimeout([]string{"--timeout", "forever"}, time.Second); err == nil {
		t.Fatal("invalid timeout accepted")
	}
}

func TestDoctorReportsReachableButUnsupervisedManagedRuntime(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, listed: []surface.Session{{ID: "thread", Surface: surface.KindCodex}}}
	runtimeSurface := &runtimeCLISurface{cliSurface: fake, status: surface.RuntimeStatus{Name: "Codex managed app-server", Reachable: true, Backend: "pid", Remediation: "agenthail launch codex"}}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = runtimeSurface
	app.daemonServiceLoaded = func() bool { return false }
	output, err := captureStdout(t, func() error { return app.cmdDoctor([]string{"--json"}) })
	if err == nil || !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("err=%v output=%s", err, output)
	}
	var payload struct {
		Surfaces []struct {
			OK      bool                  `json:"ok"`
			Runtime surface.RuntimeStatus `json:"runtime"`
		} `json:"surfaces"`
	}
	if json.Unmarshal([]byte(output), &payload) != nil || len(payload.Surfaces) != 1 {
		t.Fatalf("output=%s", output)
	}
	result := payload.Surfaces[0]
	if result.OK || !result.Runtime.Reachable || result.Runtime.Durable || result.Runtime.Remediation != "agenthail launch codex" {
		t.Fatalf("result=%+v", result)
	}
}

func TestDoctorRecognizesAgenthailSupervisionAsDurable(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex}
	runtimeSurface := &runtimeCLISurface{cliSurface: fake, status: surface.RuntimeStatus{Name: "Codex managed app-server", Reachable: true, Backend: "pid"}}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = runtimeSurface
	app.daemonServiceLoaded = func() bool { return true }
	output, err := captureStdout(t, func() error { return app.cmdDoctor([]string{"--json"}) })
	if err != nil {
		t.Fatalf("err=%v output=%s", err, output)
	}
	if !strings.Contains(output, `"durable":true`) || !strings.Contains(output, "supervised by Agenthail") {
		t.Fatalf("output=%s", output)
	}
}

func TestHomebrewDaemonServiceLoaded(t *testing.T) {
	installHomebrewLaunchctl(t)
	if !homebrewDaemonServiceLoaded() {
		t.Fatal("Homebrew service was not detected")
	}
}

func TestHomebrewDaemonManagedByExecutablePath(t *testing.T) {
	if !homebrewManagedExecutable("/opt/homebrew/Cellar/agenthail/0.1.3/libexec/agenthail") {
		t.Fatal("Homebrew Cellar executable was not detected")
	}
	if homebrewManagedExecutable("/Users/example/.local/share/agenthail/agenthail") {
		t.Fatal("source executable was detected as Homebrew")
	}
}

func TestHomebrewDaemonLifecycleUsesHomebrewSupervisor(t *testing.T) {
	logPath := installHomebrewLaunchctl(t)
	app := &App{}
	for name, check := range map[string]struct {
		call func() error
		want string
	}{
		"start":     {call: app.daemonStart, want: "brew services start agenthail"},
		"stop":      {call: app.daemonStop, want: "brew services stop agenthail"},
		"install":   {call: app.daemonInstallService, want: "brew services restart agenthail"},
		"uninstall": {call: app.daemonUninstallService, want: "brew services stop agenthail"},
	} {
		t.Run(name, func(t *testing.T) {
			err := check.call()
			if err == nil || !strings.Contains(err.Error(), check.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	for name, call := range map[string]func() error{
		"restart":   app.daemonRestart,
		"dashboard": app.restartDaemonForDashboard,
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil || !strings.Contains(err.Error(), "restart launchd service") {
				t.Fatalf("err=%v", err)
			}
		})
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "homebrew.mxcl.agenthail") != 2 {
		t.Fatalf("launchctl log=%q", data)
	}
}

func installHomebrewLaunchctl(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("launchd is only available on macOS")
	}
	dir := t.TempDir()
	launchctl := filepath.Join(dir, "launchctl")
	logPath := filepath.Join(dir, "launchctl.log")
	script := "#!/bin/sh\nif [ \"$1\" = print ]; then [ \"$2\" = \"gui/$(id -u)/homebrew.mxcl.agenthail\" ]; exit; fi\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\nexit 1\n"
	if err := os.WriteFile(launchctl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return logPath
}

func TestSubcommandSpecificFlags(t *testing.T) {
	if err := validateCommandFlags("queue", []string{"list", "--json", "--all", "--target", "@builder", "--mine", "--cwd", "/work"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"retry", "1", "--json"}, {"target", "message", "--json"}, {"list", "--from", "x"}} {
		if err := validateCommandFlags("queue", args); err == nil {
			t.Fatalf("queue flags accepted: %v", args)
		}
	}
	if err := validateCommandFlags("channel", []string{"send", "team", "message", "--from", "me"}); err != nil {
		t.Fatal(err)
	}
	if err := validateCommandFlags("channel", []string{"create", "team", "--from", "me"}); err == nil {
		t.Fatal("channel create accepted send-only flag")
	}
	if err := validateCommandFlags("dashboard", []string{"config", "--codex-recent-hours", "5"}); err != nil {
		t.Fatalf("dashboard config flag rejected: %v", err)
	}
	if err := validateCommandFlags("dashboard", []string{"remote", "--no-open", "--json", "--tailscale", "/tmp/tailscale"}); err != nil {
		t.Fatalf("dashboard remote flags rejected: %v", err)
	}
	if err := validateCommandFlags("relay", []string{"add", "from", "to", ".*", "--once"}); err != nil {
		t.Fatalf("relay once flag rejected: %v", err)
	}
}

func TestQueueListFiltersByTargetMineAndWorkspaceAncestry(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{}}
	app, r := cliFixture(t, fake)
	sessions := []surface.Session{
		{ID: "caller", Surface: surface.KindCodex, Cwd: "/work/root"},
		{ID: "nested", Surface: surface.KindCodex, Cwd: "/work/root/nested"},
		{ID: "other", Surface: surface.KindCodex, Cwd: "/work/other"},
	}
	for _, session := range sessions {
		fake.sessions[session.ID] = session
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.QueueMessageWithOptions("nested", "outbound", "", surface.SendOptions{SourceSessionID: "caller"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithOptions("caller", "inbound", "", surface.SendOptions{SourceSessionID: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithOptions("other", "unrelated", "", surface.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "caller")
	rows, err := r.ListQueue(false)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := app.filterQueueRows(context.Background(), rows, "", true, "")
	if err != nil || len(mine) != 2 {
		t.Fatalf("mine=%+v err=%v", mine, err)
	}
	nested, err := app.filterQueueRows(context.Background(), rows, "", false, "/work/root")
	if err != nil || len(nested) != 2 {
		t.Fatalf("workspace=%+v err=%v", nested, err)
	}
	target, err := app.filterQueueRows(context.Background(), rows, "codex:nested", false, "")
	if err != nil || len(target) != 1 || target[0].Message != "outbound" {
		t.Fatalf("target=%+v err=%v", target, err)
	}
}

func TestStripFlagsConsumesDashboardTailscalePath(t *testing.T) {
	got := stripFlags([]string{"remote", "--tailscale", "/Applications/Tailscale.app/Contents/MacOS/Tailscale", "--no-open"})
	if strings.Join(got, ",") != "remote" {
		t.Fatalf("positionals=%v", got)
	}
}

func TestQualifiedTargetRegistersBeforeQueue(t *testing.T) {
	session := surface.Session{ID: "fresh", Surface: surface.KindClaude, Status: surface.StatusBusy}
	fake := &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"fresh": session}}
	app, r := cliFixture(t, fake)
	if _, err := captureStdout(t, func() error { return app.cmdQueue([]string{"claude:fresh", "later"}) }); err != nil {
		t.Fatal(err)
	}
	if r.QueueCount("fresh") != 1 {
		t.Fatal("qualified target was not registered and queued")
	}
}

func TestRegisteredTargetRefreshesResolvedMetadata(t *testing.T) {
	oldLastActive := time.Now().Add(-time.Hour)
	liveLastActive := time.Now().Truncate(time.Millisecond)
	stale := surface.Session{ID: "registered", Surface: surface.KindClaude, Name: "worker", Status: surface.StatusIdle, LastActive: oldLastActive}
	live := stale
	live.Status = surface.StatusBusy
	live.LastActive = liveLastActive
	fake := &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{live.ID: live}}
	app, r := cliFixture(t, fake)
	if err := r.RegisterSession(stale); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("worker", stale.ID); err != nil {
		t.Fatal(err)
	}

	resolved, _, err := app.resolveTarget(context.Background(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := r.Session(stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != surface.StatusBusy || registered.Status != surface.StatusBusy || !registered.LastActive.Equal(liveLastActive) {
		t.Fatalf("resolved=%+v registered=%+v", resolved, registered)
	}
}

func TestCompactUsesTypedControlForWorkingClaudeSession(t *testing.T) {
	session := surface.Session{ID: "busy", Surface: surface.KindClaude, Name: "busy", Status: surface.StatusBusy}
	fake := &cliSurface{
		kind:     surface.KindClaude,
		sessions: map[string]surface.Session{"busy": session},
		caps:     surface.Capabilities{Compact: true},
	}
	app, registry := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.cmdCompact([]string{"busy"}) })
	if err != nil || !strings.Contains(output, "compact requested") || registry.QueueCount("busy") != 0 {
		t.Fatalf("output=%q queue=%d err=%v", output, registry.QueueCount("busy"), err)
	}
	if fake.compactCalls != 1 || len(fake.sent) != 0 {
		t.Fatalf("compactCalls=%d sent=%v", fake.compactCalls, fake.sent)
	}
}

func TestCompactRequestsIdleClaudeSessionWithoutWaiting(t *testing.T) {
	session := surface.Session{ID: "idle", Surface: surface.KindClaude, Name: "idle", Status: surface.StatusIdle}
	fake := &cliSurface{
		kind:        surface.KindClaude,
		sessions:    map[string]surface.Session{"idle": session},
		caps:        surface.Capabilities{Compact: true},
		observation: &surface.TurnObservation{Status: surface.StatusIdle},
		sendResult:  &surface.SendResult{UUID: "compact-turn", Accepted: true},
	}
	app, registry := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.cmdCompact([]string{"idle"}) })
	if err != nil || !strings.Contains(output, "compact requested") || registry.QueueCount("idle") != 0 {
		t.Fatalf("output=%q queue=%d err=%v", output, registry.QueueCount("idle"), err)
	}
	if fake.compactCalls != 1 || len(fake.sent) != 0 {
		t.Fatalf("compactCalls=%d sent=%v", fake.compactCalls, fake.sent)
	}
}

func TestQueueRejectsReadOnlyCodexTerminalSession(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{
		"plain": {ID: "plain", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "cli", Transport: "readOnly"},
	}}
	app, r := cliFixture(t, fake)
	err := app.cmdQueue([]string{"codex:plain", "do not queue"})
	if err == nil || !strings.Contains(err.Error(), "read only") {
		t.Fatalf("err=%v", err)
	}
	if count := r.QueueCount("plain"); count != 0 {
		t.Fatalf("queue count=%d", count)
	}
}

func TestQueueAllowsUnloadedCodexDesktopSession(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{
		"desktop": {ID: "desktop", Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded"), Source: "vscode", Transport: "desktop"},
	}}
	app, r := cliFixture(t, fake)
	if err := app.cmdQueue([]string{"codex:desktop", "deliver later"}); err != nil {
		t.Fatal(err)
	}
	if count := r.QueueCount("desktop"); count != 1 {
		t.Fatalf("queue count=%d", count)
	}
}

func TestQueueRetryRejectsReadOnlyCodexTerminalSession(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex}
	app, r := cliFixture(t, fake)
	session := surface.Session{ID: "plain", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "cli", Transport: "readOnly"}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	id, err := r.QueueMessageWithOptions(session.ID, "do not retry", "", surface.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ClaimNextMessage(session.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.NackMessage(id, errors.New("read only"), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	if err := app.cmdQueue([]string{"retry", strconv.FormatInt(id, 10)}); err == nil || !strings.Contains(err.Error(), "read only") {
		t.Fatalf("retry err=%v", err)
	}
	item, err := r.QueueItem(id)
	if err != nil || item.Status != "dead" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
}

func TestRoutingRejectsReadOnlyCodexTerminalDestination(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{
		"source": {ID: "source", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"},
		"plain":  {ID: "plain", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "cli", Transport: "readOnly"},
	}}
	app, r := cliFixture(t, fake)
	if _, err := r.CreateChannel("reviewers"); err != nil {
		t.Fatal(err)
	}
	if err := app.cmdChannel([]string{"add", "reviewers", "codex:plain"}); err == nil || !strings.Contains(err.Error(), "read only") {
		t.Fatalf("channel add err=%v", err)
	}
	if err := app.cmdRelay([]string{"add", "codex:source", "codex:plain"}); err == nil || !strings.Contains(err.Error(), "read only") {
		t.Fatalf("relay add err=%v", err)
	}
	members, err := r.ChannelMembers("reviewers")
	if err != nil || len(members) != 0 {
		t.Fatalf("members=%v err=%v", members, err)
	}
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 0 {
		t.Fatalf("routes=%v err=%v", routes, err)
	}
}

func TestRelayListShowsDerivedFiringEvidence(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex}
	app, r := cliFixture(t, fake)
	register := func(id string) {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex}); err != nil {
			t.Fatal(err)
		}
	}
	register("from")
	register("to")
	id, err := r.AddRoute("from", "to", ".*")
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := r.RecordRelayDelivery(id, "turn-one"); err != nil || !reserved {
		t.Fatalf("reserved=%v err=%v", reserved, err)
	}
	text, err := captureStdout(t, func() error { return app.Run([]string{"relay", "list"}) })
	if err != nil || !strings.Contains(text, "fires=1") || !strings.Contains(text, "last-fired=") {
		t.Fatalf("text=%q err=%v", text, err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"relay", "list", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Relays []registry.RouteRow `json:"relays"`
	}
	if err := json.Unmarshal([]byte(output), &document); err != nil || len(document.Relays) != 1 || document.Relays[0].FireCount != 1 || document.Relays[0].LastFiredAt == "" {
		t.Fatalf("document=%+v err=%v", document, err)
	}
}

func TestNotionNewRegistersPersistedThreadInsteadOfSyntheticTarget(t *testing.T) {
	const threadID = "3978aba0-0606-80ac-a1ae-00a9eb229fc0"
	synthetic := surface.Session{ID: "new:launch-notes", Surface: surface.KindNotion, Name: "launch-notes", Status: surface.StatusIdle}
	fake := &cliSurface{
		kind:       surface.KindNotion,
		sessions:   map[string]surface.Session{"new:launch-notes": synthetic},
		caps:       surface.Capabilities{Send: true},
		sendResult: &surface.SendResult{UUID: threadID, Accepted: true},
	}
	app, registry := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.cmdSend([]string{"notion:new:launch-notes", "draft", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal([]byte(output), &receipt) != nil || receipt.SessionID != threadID {
		t.Fatalf("receipt=%q", output)
	}
	registered, err := registry.Session(threadID)
	if err != nil || registered.Name != "launch-notes" || registered.Surface != surface.KindNotion {
		t.Fatalf("registered=%+v err=%v", registered, err)
	}
	if _, err := registry.Session("new:launch-notes"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("synthetic target was persisted: %v", err)
	}
	aliasID, err := registry.LookupAlias("launch-notes")
	if err != nil || aliasID != threadID {
		t.Fatalf("alias=%q err=%v", aliasID, err)
	}
}

func TestNotionNewCannotBeQueuedBeforePersistence(t *testing.T) {
	synthetic := surface.Session{ID: "new:launch-notes", Surface: surface.KindNotion, Name: "launch-notes", Status: surface.StatusIdle}
	fake := &cliSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{"new:launch-notes": synthetic}, caps: surface.Capabilities{Send: true}}
	app, registry := cliFixture(t, fake)
	err := app.cmdQueue([]string{"notion:new:launch-notes", "later"})
	if err == nil || !strings.Contains(err.Error(), "cannot be queued") {
		t.Fatalf("err=%v", err)
	}
	if _, err := registry.Session("new:launch-notes"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("synthetic target was persisted: %v", err)
	}
}

func TestSendReplyFailsClosedWhenBaselineObservationFails(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Send: true, Reply: true}, observeErr: errors.New("cursor unavailable")}
	app, _ := cliFixture(t, fake)
	err := app.cmdSend([]string{"codex:s", "hello", "--reply"})
	if err == nil || !strings.Contains(err.Error(), "establish reply cursor") || len(fake.sent) != 0 {
		t.Fatalf("err=%v sent=%v", err, fake.sent)
	}
}

func TestSendStreamPreservesFragmentBytes(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Send: true, Stream: true}, streamEvents: []surface.StreamEvent{{Kind: "text", Text: "hel"}, {Kind: "text", Text: "lo"}, {Kind: "done"}}}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.cmdSend([]string{"codex:s", "hello", "--stream"}) })
	if err != nil || output != "hello\n" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if err := app.cmdSend([]string{"codex:s", "hello", "--stream", "--json"}); err == nil {
		t.Fatal("incompatible stream JSON accepted")
	}
}

func TestWaitForReplyReportsTurnThatEndsWithoutCompletion(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, observations: []*surface.TurnObservation{
		{Status: surface.StatusBusy, ActiveTurnID: "turn", CompletedTurnID: "old", Reply: &surface.ReplyResult{Text: "old", Done: true}},
		{Status: surface.StatusIdle, CompletedTurnID: "old", Reply: &surface.ReplyResult{Text: "old", Done: true}},
	}}
	_, err := waitForReply(context.Background(), fake, &surface.Session{ID: "s"}, "old", "turn", 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "ended without a completed") {
		t.Fatalf("err=%v", err)
	}
}

func TestWaitForReplyReportsExactEmptyCodexTerminal(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, observations: []*surface.TurnObservation{
		{Status: surface.StatusIdle, TerminalTurnID: "turn", CompletedTurnID: "old", Reply: &surface.ReplyResult{Text: "old", Done: true}},
	}}
	_, err := waitForReply(context.Background(), fake, &surface.Session{ID: "s"}, "old", "turn", time.Second)
	if err == nil || !strings.Contains(err.Error(), "ended without an assistant reply") {
		t.Fatalf("err=%v", err)
	}
}

func TestSendTimeoutBoundsDelivery(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Send: true}, sendWait: true}
	app, _ := cliFixture(t, fake)
	started := time.Now()
	err := app.cmdSend([]string{"codex:s", "hello", "--timeout", "50ms"})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("elapsed=%s err=%v", time.Since(started), err)
	}
}

func TestReplyRejectsFailedCompletion(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = &cliReadSurface{cliSurface: fake, result: &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}, Reply: &surface.ReplyResult{Text: "partial", Done: true, Error: "turn failed"}, Source: "rpc"}}
	err := app.cmdReply([]string{"codex:s"})
	if err == nil || !strings.Contains(err.Error(), "did not complete successfully") {
		t.Fatalf("err=%v", err)
	}
}

func TestListJSONUsesDefaultLimit(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{}}
	for i := 0; i < 20; i++ {
		fake.listed = append(fake.listed, surface.Session{ID: string(rune('a' + i)), Surface: surface.KindCodex, LastActive: time.Now().Add(-time.Duration(i) * time.Minute)})
	}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.cmdList([]string{"--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Sessions []surface.Session `json:"sessions"`
	}
	if json.Unmarshal([]byte(output), &document) != nil || len(document.Sessions) != 15 {
		t.Fatalf("output=%s sessions=%d", output, len(document.Sessions))
	}
}

func TestListAcceptsOnlyItsWorkspaceFlags(t *testing.T) {
	if err := validateCommandFlags("list", []string{"--all", "--cwd", "/work/project", "--wide", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := validateCommandFlags("list", []string{"--cwd"}); err == nil || !strings.Contains(err.Error(), "requires a value") {
		t.Fatalf("err=%v", err)
	}
	if err := validateCommandFlags("list", []string{"--project", "/work/project"}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("err=%v", err)
	}
}

func TestListCwdFiltersByCanonicalAncestryInTextAndJSON(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	nested := filepath.Join(project, "nested")
	other := filepath.Join(root, "project-other")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	fake := &cliSurface{kind: surface.KindCodex, listed: []surface.Session{
		{ID: "root", Surface: surface.KindCodex, Name: "root", Cwd: project},
		{ID: "nested", Surface: surface.KindCodex, Name: "nested", Cwd: nested},
		{ID: "other", Surface: surface.KindCodex, Name: "other", Cwd: other},
	}}
	app, _ := cliFixture(t, fake)
	text, err := captureStdout(t, func() error { return app.cmdList([]string{"--cwd", alias}) })
	if err != nil || !strings.Contains(text, "root") || !strings.Contains(text, "nested") || strings.Contains(text, "other") {
		t.Fatalf("text=%q err=%v", text, err)
	}
	jsonText, err := captureStdout(t, func() error { return app.cmdList([]string{"--cwd", alias, "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Sessions []surface.Session `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(jsonText), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Sessions) != 2 || document.Sessions[0].ID == "other" || document.Sessions[1].ID == "other" {
		t.Fatalf("sessions=%+v", document.Sessions)
	}
	canonicalProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range document.Sessions {
		if session.ID == "root" && session.Cwd != canonicalProject {
			t.Fatalf("root cwd=%q want=%q", session.Cwd, canonicalProject)
		}
	}
}

func TestListWideAndBasenameCollisionRetainFullCwd(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, listed: []surface.Session{
		{ID: "one", Surface: surface.KindCodex, Name: "one", Cwd: "/work/one/agenthail"},
		{ID: "two", Surface: surface.KindCodex, Name: "two", Cwd: "/work/two/agenthail"},
		{ID: "three", Surface: surface.KindCodex, Name: "three", Cwd: "/work/three/" + strings.Repeat("other", 12)},
	}}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.cmdList(nil) })
	if err != nil || !strings.Contains(output, "/work/one/agenthail") || !strings.Contains(output, "/work/two/agenthail") || strings.Contains(output, "/work/three/other") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	wide, err := captureStdout(t, func() error { return app.cmdList([]string{"--wide"}) })
	if err != nil || !strings.Contains(wide, "CWD") || !strings.Contains(wide, fake.listed[2].Cwd) {
		t.Fatalf("wide=%q err=%v", wide, err)
	}
}

func TestListPartialDiscoveryReturnsSessionsAndWarningsWithoutFailure(t *testing.T) {
	working := &cliSurface{kind: surface.KindCodex, listed: []surface.Session{{ID: "live", Surface: surface.KindCodex}}}
	optionalFailure := &cliSurface{kind: surface.KindNotion, listErr: errors.New("choose a Notion space")}
	app := App{Surfaces: []SurfaceEntry{{Name: "codex", Surface: working}, {Name: "notion", Surface: optionalFailure}}}
	output, err := captureStdout(t, func() error { return app.cmdList([]string{"--json"}) })
	if err != nil || !strings.Contains(output, `"id":"live"`) || !strings.Contains(output, `"notion":"choose a Notion space"`) {
		t.Fatalf("output=%s err=%v", output, err)
	}
}

func TestQueueListHelpDocumentsScopedFilters(t *testing.T) {
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindCodex})
	output, err := captureStdout(t, func() error { return app.cmdQueue([]string{"list", "--help"}) })
	if err != nil || !strings.Contains(output, "--target") || !strings.Contains(output, "--mine") || !strings.Contains(output, "--cwd") {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestListFailsWhenEverySurfaceDiscoveryFails(t *testing.T) {
	fake := &cliSurface{kind: surface.KindNotion, listErr: errors.New("unavailable")}
	app := App{Surfaces: []SurfaceEntry{{Name: "notion", Surface: fake}}}
	_, err := captureStdout(t, func() error { return app.cmdList([]string{"--json"}) })
	if err == nil || !strings.Contains(err.Error(), "1 surface(s) failed discovery") {
		t.Fatalf("err=%v", err)
	}
}

func TestListAllIncludesSavedSessionsWithoutSurfaceDiscovery(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex}
	app, registry := cliFixture(t, fake)
	if err := registry.RegisterSession(surface.Session{ID: "old", Surface: surface.KindCodex, Name: "old project"}); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.cmdList([]string{"--all", "--json"}) })
	if err != nil || !strings.Contains(output, "old project") || fake.listCalls != 1 {
		t.Fatalf("output=%s calls=%d err=%v", output, fake.listCalls, err)
	}
}

func TestSearchCodexPrintsAndRegistersHistoryHits(t *testing.T) {
	hit := surface.Session{ID: "old", Surface: surface.KindCodex, Name: "old project"}
	fake := &cliSurface{kind: surface.KindCodex, searchResults: []surface.SessionSearchResult{{Session: hit, Snippet: "matched text"}}}
	app, registry := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.cmdSearch([]string{"codex", "project"}) })
	if err != nil || !strings.Contains(output, "old project") || !strings.Contains(output, "matched text") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if _, err := registry.Session("old"); err != nil {
		t.Fatalf("search result was not retained: %v", err)
	}
}

func TestSearchCodexRequiresThreeCharacters(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex}
	app, _ := cliFixture(t, fake)
	err := app.cmdSearch([]string{"codex", "Q"})
	if err == nil || !strings.Contains(err.Error(), "at least 3") {
		t.Fatalf("err=%v", err)
	}
}

func TestLastEmptyJSONIsOneDocument(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindNotion}
	fake := &cliSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{"s": session}}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.cmdLast([]string{"notion:s", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if json.Unmarshal([]byte(output), &document) != nil || !strings.Contains(output, `"exchanges":[]`) {
		t.Fatalf("output=%q", output)
	}
}

func TestLastLabelsTranscriptSourceInTextAndJSON(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, tail: []surface.Exchange{{User: "question", Assistant: "answer", Source: "local-transcript"}}}
	app, _ := cliFixture(t, fake)
	text, err := captureStdout(t, func() error { return app.cmdLast([]string{"codex:s"}) })
	if err != nil || !strings.Contains(text, "[local-transcript]") {
		t.Fatalf("text=%q err=%v", text, err)
	}
	output, err := captureStdout(t, func() error { return app.cmdLast([]string{"codex:s", "--json"}) })
	if err != nil || !strings.Contains(output, `"source":"local-transcript"`) {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestLastPassesExplicitOlderCursorAndReportsNextPage(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}}
	reader := &cliReadSurface{cliSurface: fake, result: &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{{Assistant: "older", Source: "local-transcript"}}, Source: "local-transcript", NextBefore: 123}}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = reader
	output, err := captureStdout(t, func() error { return app.cmdLast([]string{"codex:s", "--before", "456", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.requests) != 1 || reader.requests[0].Before != 456 || !strings.Contains(output, `"nextBefore":123`) {
		t.Fatalf("requests=%+v output=%s", reader.requests, output)
	}
}

func TestReplyLabelsSourceAndBoundsDeadline(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}, tail: []surface.Exchange{{User: "question", Assistant: "answer", Source: "rpc"}}}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.cmdReply([]string{"codex:s", "--json", "--timeout", "50ms"}) })
	if err != nil || !strings.Contains(output, `"source":"rpc"`) {
		t.Fatalf("output=%q err=%v", output, err)
	}
	blockedTail := make(chan struct{})
	defer close(blockedTail)
	blocked := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}, tailBlock: blockedTail}
	app, _ = cliFixture(t, blocked)
	started := time.Now()
	err = app.cmdReply([]string{"codex:s", "--timeout", "50ms"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("elapsed=%s err=%v", time.Since(started), err)
	}
}

func TestLastTimeoutBoundsTail(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindNotion}
	blocked := make(chan struct{})
	defer close(blocked)
	fake := &cliSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{"s": session}, tailBlock: blocked}
	app, _ := cliFixture(t, fake)
	started := time.Now()
	err := app.cmdLast([]string{"notion:s", "--timeout", "50ms"})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("elapsed=%s err=%v", time.Since(started), err)
	}
}

func TestUnicodeTruncationIsValid(t *testing.T) {
	if got := truncate("a界b", 3); got != "a界b" {
		t.Fatalf("got=%q", got)
	}
	if got := truncate("a界bc", 3); got != "a界…" {
		t.Fatalf("got=%q", got)
	}
}

func TestCodexLaunchEnablesLoopbackRendererDebugging(t *testing.T) {
	t.Setenv("AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT", "9333")
	args := strings.Join(codexLaunchArgs(), " ")
	if strings.Contains(args, "--inspect") || !strings.Contains(args, "--remote-debugging-address=127.0.0.1") || !strings.Contains(args, "--remote-debugging-port=9333") {
		t.Fatalf("launch args=%q", args)
	}
}

func TestRotateLogFileOnlyMovesLogsAboveLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	if err := os.WriteFile(path, []byte("small"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rotateLogFile(path, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log at limit should remain: %v", err)
	}
	if err := os.WriteFile(path, []byte("too large"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rotateLogFile(path, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("oversized active log should move, stat err=%v", err)
	}
	if data, err := os.ReadFile(path + ".1"); err != nil || string(data) != "too large" {
		t.Fatalf("backup=%q err=%v", data, err)
	}
}

func TestParseDaemonServicePID(t *testing.T) {
	output := "com.agenthail.daemon = {\n\tstate = running\n\tpid = 88284\n}"
	if pid := parseDaemonServicePID(output); pid != 88284 {
		t.Fatalf("pid=%d", pid)
	}
	if pid := parseDaemonServicePID("state = waiting\n"); pid != 0 {
		t.Fatalf("waiting service pid=%d", pid)
	}
}

func TestSelectCodexPIDValidatesExecutableAndMultipleResults(t *testing.T) {
	pid := selectCodexPID(`bad
12 /Applications/Other.app/Contents/MacOS/ChatGPT
34 /Applications/Codex.app/Contents/MacOS/ChatGPT --flag
`, []string{"/Applications/ChatGPT.app/Contents/MacOS/ChatGPT", "/Applications/Codex.app/Contents/MacOS/ChatGPT"})
	if pid != 34 {
		t.Fatalf("pid=%d", pid)
	}
}
