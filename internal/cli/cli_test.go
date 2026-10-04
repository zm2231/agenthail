package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

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
	sendErr       error
	reply         *surface.ReplyResult
	replyWait     bool
	sendWait      bool
	tailBlock     <-chan struct{}
	sent          []string
	steerSource   string
	steered       []string
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

func (f *runtimeCLISurface) RuntimeStatus(context.Context) surface.RuntimeStatus { return f.status }

func TestCodexCommandRejectsCustomRemote(t *testing.T) {
	err := (&App{}).Run([]string{"codex", "--remote", "ws://example.test"})
	if err == nil || !strings.Contains(err.Error(), "manages the remote transport") {
		t.Fatalf("err=%v", err)
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
	if f.sendErr != nil {
		return nil, f.sendErr
	}
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
func (*cliSurface) Interrupt(context.Context, *surface.Session) error { return nil }
func (f *cliSurface) Steer(ctx context.Context, _ *surface.Session, message string) error {
	f.steerSource = surface.SourceSessionID(ctx)
	f.steered = append(f.steered, message)
	return nil
}
func (f *cliSurface) Capabilities() surface.Capabilities { return f.caps }
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
	return &App{Registry: r, Surfaces: []SurfaceEntry{{Name: string(fake.kind), Surface: fake}}, DefaultTimeout: time.Second, catalogDaemonRunning: func() bool { return false }}, r
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

func TestCmdSteerUsesResolvedSourceAndUnifiedReceipt(t *testing.T) {
	fake := &cliSurface{
		kind: surface.KindCodex,
		sessions: map[string]surface.Session{
			"target": {ID: "target", Name: "Target", Surface: surface.KindCodex, Source: "vscode", Transport: "desktop", Status: surface.StatusBusy},
		},
		caps: surface.Capabilities{Steer: true},
	}
	app, r := cliFixture(t, fake)
	source := surface.Session{ID: "source", Name: "Source", Surface: surface.KindCodex, Source: "vscode", Transport: "desktop", Status: surface.StatusIdle}
	if err := r.RegisterSession(source); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(fake.sessions["target"]); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "source")
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"steer", "target", "focus now"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.steerSource != "source" || len(fake.steered) != 1 || fake.steered[0] != "focus now" {
		t.Fatalf("source=%q steered=%v", fake.steerSource, fake.steered)
	}
	if output != "Sent to codex/Target.\n" {
		t.Fatalf("output=%q", output)
	}
	intent, err := r.DeliveryIntent(1)
	if err != nil || intent.SenderSessionID != "source" || intent.Status != registry.DeliveryIntentSent {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}

func TestSendFlagValidation(t *testing.T) {
	for _, test := range []struct {
		name      string
		args      []string
		wantError string
	}{
		{"unknown", []string{"--bogus"}, "unknown flag"},
		{"missing value", []string{"--model", "--json"}, "requires a value"},
		{"duplicate", []string{"--reply", "--reply"}, "only be specified once"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (&App{}).Run(append([]string{"send"}, test.args...))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"target": {ID: "target", Surface: surface.KindCodex, Status: surface.StatusIdle}}, caps: surface.Capabilities{Send: true}}
	app, _ := cliFixture(t, fake)
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"send", "codex:target", "message", "--no-queue", "--json"})
	}); err != nil {
		t.Fatalf("--no-queue --json rejected: %v", err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "codex:target", "--", "--literal"}) }); err != nil {
		t.Fatalf("terminator rejected: %v", err)
	}
	if len(fake.sent) != 2 || fake.sent[1] != "--literal" {
		t.Fatalf("sent=%q", fake.sent)
	}
}

func TestDoctorReportsReachableButUnsupervisedManagedRuntime(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, listed: []surface.Session{{ID: "thread", Surface: surface.KindCodex}}}
	runtimeSurface := &runtimeCLISurface{cliSurface: fake, status: surface.RuntimeStatus{Name: "Codex managed app-server", Reachable: true, Backend: "pid", Remediation: "agenthail launch codex"}}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = runtimeSurface
	app.daemonServiceLoaded = func() bool { return false }
	output, err := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
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
	output, err := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
	if err != nil {
		t.Fatalf("err=%v output=%s", err, output)
	}
	if !strings.Contains(output, `"durable":true`) || !strings.Contains(output, "supervised by Agenthail") {
		t.Fatalf("output=%s", output)
	}
}

func TestHomebrewDaemonLifecycleUsesHomebrewSupervisor(t *testing.T) {
	logPath := installHomebrewLaunchctl(t)
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	for name, check := range map[string]struct {
		args []string
		want string
	}{
		"start":     {args: []string{"daemon", "start"}, want: "brew services start agenthail"},
		"stop":      {args: []string{"daemon", "stop"}, want: "brew services stop agenthail"},
		"install":   {args: []string{"daemon", "install"}, want: "brew services restart agenthail"},
		"uninstall": {args: []string{"daemon", "uninstall"}, want: "brew services stop agenthail"},
		"restart":   {args: []string{"daemon", "restart"}, want: "restart launchd service"},
		"dashboard": {args: []string{"dashboard", "enable", "--no-open"}, want: "restart launchd service"},
	} {
		t.Run(name, func(t *testing.T) {
			err := app.Run(check.args)
			if err == nil || !strings.Contains(err.Error(), check.want) {
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
	list := func(filters ...string) []string {
		t.Helper()
		output, err := captureStdout(t, func() error { return app.Run(append([]string{"queue", "list", "--json"}, filters...)) })
		if err != nil {
			t.Fatalf("filters=%v err=%v", filters, err)
		}
		var document struct {
			Messages []struct {
				Message string `json:"message"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(output), &document); err != nil {
			t.Fatalf("output=%s err=%v", output, err)
		}
		var messages []string
		for _, row := range document.Messages {
			messages = append(messages, row.Message)
		}
		sort.Strings(messages)
		return messages
	}
	if got := list("--mine"); strings.Join(got, ",") != "inbound,outbound" {
		t.Fatalf("mine=%v", got)
	}
	if got := list("--cwd", "/work/root"); strings.Join(got, ",") != "inbound,outbound" {
		t.Fatalf("workspace=%v", got)
	}
	if got := list("--target", "codex:nested"); strings.Join(got, ",") != "outbound" {
		t.Fatalf("target=%v", got)
	}
}

func TestQualifiedTargetRegistersBeforeQueue(t *testing.T) {
	session := surface.Session{ID: "fresh", Surface: surface.KindClaude, Status: surface.StatusBusy}
	fake := &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"fresh": session}}
	app, r := cliFixture(t, fake)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"queue", "claude:fresh", "later"}) }); err != nil {
		t.Fatal(err)
	}
	if r.QueueCount("fresh") != 1 {
		t.Fatal("qualified target was not registered and queued")
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
	output, err := captureStdout(t, func() error { return app.Run([]string{"compact", "busy"}) })
	if err != nil || !strings.Contains(output, "compact queued") || registry.QueueCount("busy") != 1 {
		t.Fatalf("output=%q queue=%d err=%v", output, registry.QueueCount("busy"), err)
	}
	if fake.compactCalls != 0 || len(fake.sent) != 0 {
		t.Fatalf("compactCalls=%d sent=%v", fake.compactCalls, fake.sent)
	}
}

func TestQueueRejectsReadOnlyCodexTerminalSession(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{
		"plain": {ID: "plain", Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "cli", Transport: "readOnly"},
	}}
	app, r := cliFixture(t, fake)
	err := app.Run([]string{"queue", "codex:plain", "do not queue"})
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
	if err := app.Run([]string{"queue", "codex:desktop", "deliver later"}); err != nil {
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
	if err := app.Run([]string{"queue", "retry", strconv.FormatInt(id, 10)}); err == nil || !strings.Contains(err.Error(), "read only") {
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
	if err := app.Run([]string{"channel", "add", "reviewers", "codex:plain"}); err == nil || !strings.Contains(err.Error(), "read only") {
		t.Fatalf("channel add err=%v", err)
	}
	if err := app.Run([]string{"relay", "add", "codex:source", "codex:plain"}); err == nil || !strings.Contains(err.Error(), "read only") {
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
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "notion:new:launch-notes", "draft", "--json"}) })
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
	err := app.Run([]string{"queue", "notion:new:launch-notes", "later"})
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
	err := app.Run([]string{"send", "codex:s", "hello", "--reply"})
	if err == nil || !strings.Contains(err.Error(), "establish reply cursor") || len(fake.sent) != 0 {
		t.Fatalf("err=%v sent=%v", err, fake.sent)
	}
}

func TestSendStreamPreservesFragmentBytes(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Send: true, Stream: true}, streamEvents: []surface.StreamEvent{{Kind: "text", Text: "hel"}, {Kind: "text", Text: "lo"}, {Kind: "done"}}}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "codex:s", "hello", "--stream"}) })
	if err != nil || output != "hello\n" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if err := app.Run([]string{"send", "codex:s", "hello", "--stream", "--json"}); err == nil {
		t.Fatal("incompatible stream JSON accepted")
	}
}

func TestSendDirectStreamDisplaysNormalizedClaudeTimelineAndFailsTerminal(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindClaude}
	fake := &cliSurface{
		kind:       surface.KindClaude,
		sessions:   map[string]surface.Session{"s": session},
		caps:       surface.Capabilities{Send: true, Stream: true},
		sendResult: &surface.SendResult{UUID: "turn-a", Accepted: true},
		streamEvents: []surface.StreamEvent{
			{ID: "user", Role: "user", Kind: "message", Text: "do not print"},
			{ID: "answer", Role: "assistant", Kind: "message", Text: "answer", Final: true, Version: 6},
			{ID: "call", Role: "assistant", Kind: "toolCall", Text: "Read x", CallID: "call-1"},
			{ID: "result", Role: "user", Kind: "toolResult", Text: "contents", CallID: "call-1"},
			{Kind: "done", Status: "cancelled"},
		},
	}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "claude:s", "hello", "--stream"}) })
	if err == nil || !strings.Contains(err.Error(), "did not complete successfully: cancelled") || output != "answer  -> Read x\n  <- contents\n" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestSendDirectStreamPreservesAppendThenAuthoritativeFinal(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{
		kind:       surface.KindCodex,
		sessions:   map[string]surface.Session{"s": session},
		caps:       surface.Capabilities{Send: true, Stream: true},
		sendResult: &surface.SendResult{UUID: "turn-a", Accepted: true},
		streamEvents: []surface.StreamEvent{
			{ID: "managed:turn-a:text", Operation: "append", Version: 3, Kind: "text", Text: "hel"},
			{ID: "managed:turn-a:text", Operation: "append", Version: 5, Kind: "text", Text: "lo"},
			{ID: "managed:turn-a:text", Operation: "upsert", Version: 11, Final: true, Kind: "message", Role: "assistant", Text: "hello final"},
			{Kind: "done"},
		},
	}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "codex:s", "hello", "--stream"}) })
	if err != nil || output != "hello final\n" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestSendTimeoutBoundsDelivery(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Send: true}, sendWait: true}
	app, _ := cliFixture(t, fake)
	started := time.Now()
	err := app.Run([]string{"send", "codex:s", "hello", "--timeout", "50ms"})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("elapsed=%s err=%v", time.Since(started), err)
	}
}

func TestReplyRejectsFailedCompletion(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = &cliReadSurface{cliSurface: fake, result: &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}, Reply: &surface.ReplyResult{Text: "partial", Done: true, Error: "turn failed"}, Source: "rpc"}}
	err := app.Run([]string{"reply", "codex:s"})
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
	output, err := captureStdout(t, func() error { return app.Run([]string{"list", "--json"}) })
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
	text, err := captureStdout(t, func() error { return app.Run([]string{"list", "--cwd", alias}) })
	if err != nil || !strings.Contains(text, "root") || !strings.Contains(text, "nested") || strings.Contains(text, "other") {
		t.Fatalf("text=%q err=%v", text, err)
	}
	jsonText, err := captureStdout(t, func() error { return app.Run([]string{"list", "--cwd", alias, "--json"}) })
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

func TestListUsesDaemonCatalogWithoutProviderCallsAndPreservesFilters(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	other := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	fake := &cliSurface{kind: surface.KindCodex, listed: []surface.Session{{ID: "provider-call-would-be-a-bug", Surface: surface.KindCodex}}}
	app, store := cliFixture(t, fake)
	app.catalogDaemonRunning = func() bool { return true }
	if err := store.EnsureCatalogState(); err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, item := range []surface.Session{
		{ID: "catalog-root", Surface: surface.KindCodex, Name: "root", Cwd: root, LastActive: observedAt},
		{ID: "catalog-nested", Surface: surface.KindCodex, Name: "nested", Cwd: nested, LastActive: observedAt.Add(-time.Minute)},
		{ID: "catalog-other", Surface: surface.KindCodex, Name: "other", Cwd: other, LastActive: observedAt.Add(-2 * time.Minute)},
	} {
		if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{
			Session:               item,
			HostProject:           json.RawMessage(`{}`),
			Checkout:              json.RawMessage(`{}`),
			ObservedAt:            observedAt,
			ProjectionFingerprint: item.ID,
		}, registry.CatalogEvent{DedupeKey: "session:" + item.ID, Type: "session.upserted", EntityID: item.ID, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := store.RecordCatalogSurface(registry.CatalogSurfaceState{Surface: surface.KindCodex, Health: "healthy", ObservedAt: observedAt}, registry.CatalogEvent{DedupeKey: "surface:codex:healthy", Type: "surface.health", EntityID: string(surface.KindCodex), Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}

	output, err := captureStdout(t, func() error { return app.Run([]string{"list", "--cwd", root, "--wide", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if fake.listCalls != 0 {
		t.Fatalf("provider list calls=%d, want zero while daemon catalog is authoritative", fake.listCalls)
	}
	var document struct {
		Sessions []surface.Session `json:"sessions"`
		Catalog  struct {
			Source     string `json:"source"`
			CatalogSeq uint64 `json:"catalogSeq"`
			Surfaces   []struct {
				Health string `json:"health"`
			} `json:"surfaces"`
			Freshness map[string]struct {
				Generation uint64    `json:"generation"`
				ObservedAt time.Time `json:"observedAt"`
				Stale      bool      `json:"stale"`
			} `json:"freshness"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatal(err)
	}
	if document.Catalog.Source != "daemon" || document.Catalog.CatalogSeq == 0 || len(document.Catalog.Surfaces) != 1 || document.Catalog.Surfaces[0].Health != "healthy" {
		t.Fatalf("catalog metadata=%+v", document.Catalog)
	}
	if len(document.Sessions) != 2 || document.Sessions[0].ID == "catalog-other" || document.Sessions[1].ID == "catalog-other" {
		t.Fatalf("sessions=%+v", document.Sessions)
	}
	if got := document.Catalog.Freshness["catalog-root"].ObservedAt; !got.Equal(observedAt) {
		t.Fatalf("root freshness=%s want=%s", got, observedAt)
	}
	if got := document.Catalog.Freshness["catalog-root"]; got.Generation != 1 || got.Stale {
		t.Fatalf("root freshness=%+v", got)
	}
}

func TestListWideAndBasenameCollisionRetainFullCwd(t *testing.T) {
	fake := &cliSurface{kind: surface.KindCodex, listed: []surface.Session{
		{ID: "one", Surface: surface.KindCodex, Name: "one", Cwd: "/work/one/agenthail"},
		{ID: "two", Surface: surface.KindCodex, Name: "two", Cwd: "/work/two/agenthail"},
		{ID: "three", Surface: surface.KindCodex, Name: "three", Cwd: "/work/three/" + strings.Repeat("other", 12)},
	}}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.Run([]string{"list"}) })
	if err != nil || !strings.Contains(output, "/work/one/agenthail") || !strings.Contains(output, "/work/two/agenthail") || strings.Contains(output, "/work/three/other") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	wide, err := captureStdout(t, func() error { return app.Run([]string{"list", "--wide"}) })
	if err != nil || !strings.Contains(wide, "CWD") || !strings.Contains(wide, fake.listed[2].Cwd) {
		t.Fatalf("wide=%q err=%v", wide, err)
	}
}

func TestListPartialDiscoveryReturnsSessionsAndWarningsWithoutFailure(t *testing.T) {
	working := &cliSurface{kind: surface.KindCodex, listed: []surface.Session{{ID: "live", Surface: surface.KindCodex}}}
	optionalFailure := &cliSurface{kind: surface.KindNotion, listErr: errors.New("choose a Notion space")}
	app := App{Surfaces: []SurfaceEntry{{Name: "codex", Surface: working}, {Name: "notion", Surface: optionalFailure}}}
	output, err := captureStdout(t, func() error { return app.Run([]string{"list", "--json"}) })
	if err != nil || !strings.Contains(output, `"id":"live"`) || !strings.Contains(output, `"notion":"choose a Notion space"`) {
		t.Fatalf("output=%s err=%v", output, err)
	}
}

func TestListFailsWhenEverySurfaceDiscoveryFails(t *testing.T) {
	fake := &cliSurface{kind: surface.KindNotion, listErr: errors.New("unavailable")}
	app := App{Surfaces: []SurfaceEntry{{Name: "notion", Surface: fake}}}
	_, err := captureStdout(t, func() error { return app.Run([]string{"list", "--json"}) })
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
	output, err := captureStdout(t, func() error { return app.Run([]string{"list", "--all", "--json"}) })
	if err != nil || !strings.Contains(output, "old project") || fake.listCalls != 1 {
		t.Fatalf("output=%s calls=%d err=%v", output, fake.listCalls, err)
	}
}

func TestSearchCodexPrintsAndRegistersHistoryHits(t *testing.T) {
	hit := surface.Session{ID: "old", Surface: surface.KindCodex, Name: "old project"}
	fake := &cliSurface{kind: surface.KindCodex, searchResults: []surface.SessionSearchResult{{Session: hit, Snippet: "matched text"}}}
	app, registry := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.Run([]string{"search", "codex", "project"}) })
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
	err := app.Run([]string{"search", "codex", "Q"})
	if err == nil || !strings.Contains(err.Error(), "at least 3") {
		t.Fatalf("err=%v", err)
	}
}

func TestLastEmptyJSONIsOneDocument(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindNotion}
	fake := &cliSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{"s": session}}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.Run([]string{"last", "notion:s", "--json"}) })
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
	text, err := captureStdout(t, func() error { return app.Run([]string{"last", "codex:s"}) })
	if err != nil || !strings.Contains(text, "[local-transcript]") {
		t.Fatalf("text=%q err=%v", text, err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"last", "codex:s", "--json"}) })
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
	output, err := captureStdout(t, func() error { return app.Run([]string{"last", "codex:s", "--before", "456", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.requests) != 1 || reader.requests[0].Before != 456 || !strings.Contains(output, `"nextBefore":123`) {
		t.Fatalf("requests=%+v output=%s", reader.requests, output)
	}
}

func TestLastKeepsExchangesWhenOnlyDetailIsUnavailableAndSurfacesWarning(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}}
	reader := &cliReadSurface{cliSurface: fake, result: &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{{User: "q", Assistant: "a", Source: "rpc"}}, Source: "rpc", UnavailableReason: "no local transcript yet"}}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = reader
	output, err := captureStdout(t, func() error { return app.Run([]string{"last", "codex:s", "--json"}) })
	if err != nil || !strings.Contains(output, `"assistant":"a"`) || !strings.Contains(output, `"readError":"no local transcript yet"`) {
		t.Fatalf("output=%q err=%v", output, err)
	}
	reader.result = &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}, Source: "local-transcript", UnavailableReason: "no local transcript yet"}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"last", "codex:s", "--json"}) }); err == nil || !strings.Contains(err.Error(), "no local transcript yet") {
		t.Fatalf("err=%v", err)
	}
	reader.result = &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{{Assistant: "a", Source: "local-transcript"}}, Source: "local-transcript", Warning: "native read failed"}
	output, err = captureStdout(t, func() error { return app.Run([]string{"last", "codex:s", "--json"}) })
	if err != nil || !strings.Contains(output, `"warning":"native read failed"`) {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestReplyLabelsSourceAndBoundsDeadline(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}, tail: []surface.Exchange{{User: "question", Assistant: "answer", Source: "rpc"}}}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.Run([]string{"reply", "codex:s", "--json", "--timeout", "50ms"}) })
	if err != nil || !strings.Contains(output, `"source":"rpc"`) {
		t.Fatalf("output=%q err=%v", output, err)
	}
	blockedTail := make(chan struct{})
	defer close(blockedTail)
	blocked := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Reply: true}, tailBlock: blockedTail}
	app, _ = cliFixture(t, blocked)
	started := time.Now()
	err = app.Run([]string{"reply", "codex:s", "--timeout", "50ms"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("elapsed=%s err=%v", time.Since(started), err)
	}
}

func TestCodexLaunchEnablesLoopbackRendererDebugging(t *testing.T) {
	t.Setenv("AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT", "9333")
	args := strings.Join(codexLaunchArgs(), " ")
	if strings.Contains(args, "--inspect") || !strings.Contains(args, "--remote-debugging-address=127.0.0.1") || !strings.Contains(args, "--remote-debugging-port=9333") {
		t.Fatalf("launch args=%q", args)
	}
}

func TestManagedCodexLaunchWritesProviderReceiptBeforeExec(t *testing.T) {
	receiptPath := filepath.Join(t.TempDir(), "launch.json")
	var prepared struct {
		cwd   string
		model string
	}
	var executed struct {
		path string
		argv []string
	}
	var receiptAtExec surface.ManagedCodexLaunchReceipt
	errExec := errors.New("provider process exited after dispatch")
	err := runManagedCodexLaunch(
		context.Background(),
		[]string{"--model", "o4-mini", "--", "hello"},
		"/work/project", "agenthail-launch", receiptPath,
		managedCodexLaunchBinding{Runtime: surface.LauncherCMUX, Workspace: "workspace:7", Surface: "surface:9"},
		func(_ context.Context, cwd, model string) (*surface.Session, error) {
			prepared.cwd, prepared.model = cwd, model
			return &surface.Session{ID: "provider-thread"}, nil
		},
		func(path string, argv, _ []string) error {
			executed.path, executed.argv = path, append([]string(nil), argv...)
			var readErr error
			receiptAtExec, readErr = surface.ReadManagedCodexLaunchReceipt(receiptPath)
			if readErr != nil {
				t.Fatalf("receipt was not durable before exec: %v", readErr)
			}
			return errExec
		},
		"/usr/local/bin/codex",
	)
	if !errors.Is(err, errExec) {
		t.Fatalf("err=%v, want provider exec error", err)
	}
	if prepared.cwd != "/work/project" || prepared.model != "o4-mini" {
		t.Fatalf("prepared=%+v", prepared)
	}
	if executed.path != "/usr/local/bin/codex" || !reflect.DeepEqual(executed.argv, []string{"codex", "resume", "provider-thread", "--remote", "unix://", "--", "hello"}) {
		t.Fatalf("executed=%+v", executed)
	}
	if receiptAtExec.LaunchID != "agenthail-launch" || receiptAtExec.ThreadID != "provider-thread" || receiptAtExec.Workspace != "workspace:7" || receiptAtExec.Surface != "surface:9" || receiptAtExec.TmuxPane != "" {
		t.Fatalf("receipt=%+v", receiptAtExec)
	}
}
