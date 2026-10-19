package surfaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexRouteFollowsTheLiveWriterThenTheRecordedCreator(t *testing.T) {
	bridge := &desktopCodexClient{}
	daemon := &managedCodexClient{}
	for _, test := range []struct {
		name       string
		runtimes   codexRuntimes
		originator string
		want       string
	}{
		{name: "loaded on Desktop", runtimes: codexRuntimes{desktop: bridge, desktopLoaded: map[string]bool{"t": true}}, originator: "agenthail", want: codexTransportDesktop},
		{name: "loaded on the daemon", runtimes: codexRuntimes{desktop: bridge, managedLoaded: map[string]bool{"t": true}}, originator: codexDesktopOriginator, want: codexTransportManaged},
		{name: "unloaded Agenthail thread without Desktop", runtimes: codexRuntimes{}, originator: "agenthail", want: codexTransportManaged},
		{name: "unloaded terminal thread with Desktop", runtimes: codexRuntimes{desktop: bridge, managed: daemon}, originator: "codex-tui", want: codexTransportManaged},
		{name: "unloaded Desktop thread with Desktop", runtimes: codexRuntimes{desktop: bridge, managed: daemon}, originator: codexDesktopOriginator, want: codexTransportDesktop},
		{name: "unloaded Desktop thread without Desktop", runtimes: codexRuntimes{managed: daemon}, originator: codexDesktopOriginator, want: codexTransportReadOnly},
		{name: "unknown creator with Desktop", runtimes: codexRuntimes{desktop: bridge, managed: daemon}, want: codexTransportDesktop},
		{name: "unknown creator without Desktop", runtimes: codexRuntimes{managed: daemon}, want: codexTransportReadOnly},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.runtimes.route("t", test.originator); got != test.want {
				t.Fatalf("route=%q, want %q", got, test.want)
			}
		})
	}
}

func TestCodexThreadOriginatorFallsBackToTheRolloutSessionMeta(t *testing.T) {
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"session_meta","payload":{"id":"t","originator":"codex-tui","source":"vscode"}}`+"\n"+`{"type":"event_msg","payload":{}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := codexThreadOriginator(map[string]any{"originator": "Codex Desktop", "path": rollout}); got != "Codex Desktop" {
		t.Fatalf("originator=%q, want the thread field", got)
	}
	if got := codexThreadOriginator(map[string]any{"path": rollout}); got != "codex-tui" {
		t.Fatalf("originator=%q, want the rollout session_meta", got)
	}
	if got := codexThreadOriginator(map[string]any{"path": filepath.Join(t.TempDir(), "missing.jsonl")}); got != "" {
		t.Fatalf("originator=%q, want unknown", got)
	}
}

func TestCodexDiscoveryAndReadsNeverStartTheDaemon(t *testing.T) {
	home := codextest.Home(t)
	lifecycle := codextest.InstallLifecycle(t, home, nil)
	codex := isolatedManagedRuntime(t)
	session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: codexTransportManaged}
	if _, err := codex.List(context.Background()); err == nil {
		t.Fatal("List succeeded without any Codex runtime")
	}
	if err := codex.Ready(context.Background()); err == nil {
		t.Fatal("Ready succeeded without any Codex runtime")
	}
	_, _ = codex.Resolve(context.Background(), managedThreadID)
	_, _ = codex.Observe(context.Background(), session)
	_, _ = codex.ReadSession(context.Background(), session, surface.SessionReadRequest{Limit: 5})
	_, _ = codex.Models(context.Background())
	if commands := lifecycle.Commands(); len(commands) != 0 {
		t.Fatalf("discovery or a read ran %v", commands)
	}
}

func TestCodexEnsureRuntimeStartsMissingDaemonOnce(t *testing.T) {
	lifecycle := codextest.InstallLifecycle(t, codextest.Home(t), nil)
	codex := isolatedManagedRuntime(t)
	for range 2 {
		if err := codex.EnsureRuntime(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if commands := lifecycle.Commands(); len(commands) != 1 || commands[0] != "app-server daemon start" {
		t.Fatalf("lifecycle commands=%v, want exactly one start", commands)
	}
}

func TestCodexConcurrentEnsureRuntimeStartsTheDaemonOnce(t *testing.T) {
	lifecycle := codextest.InstallLifecycle(t, codextest.Home(t), nil)
	codex := isolatedManagedRuntime(t)
	errs := make(chan error, 8)
	for range cap(errs) {
		go func() { errs <- codex.EnsureRuntime(context.Background()) }()
	}
	for range cap(errs) {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if commands := lifecycle.Commands(); len(commands) != 1 {
		t.Fatalf("lifecycle commands=%v, want exactly one start", commands)
	}
}

func TestCodexDesktopCreatedThreadIsNeverLoadedIntoTheDaemon(t *testing.T) {
	fake := startManagedCodex(t, nil)
	fake.AddThread(codextest.Thread{ID: "desktop-thread", Originator: codexDesktopOriginator})
	codex := isolatedManagedRuntime(t)
	session := &surface.Session{ID: "desktop-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: codexTransportManaged}
	_, err := codex.Send(context.Background(), session, "must stay with Desktop")
	if !surface.IsDeliveryUnavailable(err) || surface.IsDeliveryOutcomeUnknown(err) || session.Transport != codexTransportReadOnly {
		t.Fatalf("err=%v transport=%q", err, session.Transport)
	}
	if resumes := fake.Calls("thread/resume"); len(resumes) != 0 || fake.Loaded("desktop-thread") {
		t.Fatalf("the daemon loaded a Desktop thread: %v", fake.Methods())
	}
	if err := codex.EnsureWritable(context.Background(), session); err == nil || !strings.Contains(err.Error(), "agenthail launch codex") {
		t.Fatalf("err=%v", err)
	}
}

func TestCodexResumeWriterConflictIsTerminalAndOtherFailuresNeverStartATurn(t *testing.T) {
	for _, test := range []struct {
		name     string
		message  string
		terminal bool
	}{
		{name: "codex 0.151 writer conflict", message: "thread 019f004a-0000-7000-8000-00000000beef already has an active writer", terminal: true},
		{name: "codex 0.163 writer conflict", message: "thread 019f004a-0000-7000-8000-00000000beef already has an active writer", terminal: true},
		{name: "unrecognized resume failure", message: "failed to load rollout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
				if method == "thread/resume" {
					return map[string]any{"error": map[string]any{"code": -32600, "message": test.message}}
				}
				return nil
			})
			fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail"})
			_, err := isolatedManagedRuntime(t).Send(context.Background(), &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}, "hello")
			if err == nil || surface.IsDeliveryOutcomeUnknown(err) || len(fake.Calls("turn/start")) != 0 {
				t.Fatalf("err=%v methods=%v", err, fake.Methods())
			}
			if surface.IsDeliveryTerminal(err) != test.terminal || surface.IsDeliveryUnavailable(err) == test.terminal {
				t.Fatalf("err=%v terminal=%v unavailable=%v", err, surface.IsDeliveryTerminal(err), surface.IsDeliveryUnavailable(err))
			}
		})
	}
}

func TestCodexUncertainTurnWritesAreUnknown(t *testing.T) {
	for _, method := range []string{"turn/start", "turn/steer"} {
		t.Run(method, func(t *testing.T) {
			fake := startManagedCodex(t, func(requested string, _ map[string]any) map[string]any {
				if requested == method {
					return codextest.Drop
				}
				return nil
			})
			fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Turns: []codextest.Turn{{ID: "turn-1", Status: "inProgress"}}})
			codex := isolatedManagedRuntime(t)
			session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}
			var err error
			if method == "turn/start" {
				fake.CompleteTurn(managedThreadID, "done")
				_, err = codex.Send(context.Background(), session, "hello")
			} else {
				err = codex.Steer(context.Background(), session, "focus")
			}
			if !surface.IsDeliveryOutcomeUnknown(err) {
				t.Fatalf("err=%v, want unknown", err)
			}
		})
	}
}

func TestCodexUnknownLoadedStateFailsClosedForWrites(t *testing.T) {
	loadedListFails := func(method string, _ map[string]any) map[string]any {
		if method == "thread/loaded/list" {
			return map[string]any{"error": map[string]any{"code": -32603, "message": "loaded list unavailable"}}
		}
		return nil
	}
	for _, test := range []struct {
		name    string
		respond codextest.Responder
		desktop func(string) string
	}{
		{name: "daemon loaded list fails", respond: loadedListFails},
		{name: "Desktop loaded list fails", desktop: func(method string) string {
			if method == "thread/loaded/list" {
				return `{"error":{"code":-32603,"message":"loaded list unavailable"}}`
			}
			return ""
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := startManagedCodex(t, test.respond)
			fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail"})
			codex := isolatedManagedRuntime(t)
			var desktop *codextest.Desktop
			if test.desktop != nil {
				desktop = startDesktopBridge(t, test.desktop)
				codex = NewCodex(desktop.URL)
			}
			session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: codexTransportManaged}
			_, err := codex.Send(context.Background(), session, "hello")
			if !surface.IsDeliveryUnavailable(err) || surface.IsDeliveryTerminal(err) || !strings.Contains(err.Error(), "cannot tell which Codex runtime owns") {
				t.Fatalf("err=%v", err)
			}
			if err := codex.EnsureWritable(context.Background(), session); err == nil {
				t.Fatal("EnsureWritable accepted a session whose owner is unknown")
			}
			for _, method := range []string{"thread/resume", "turn/start"} {
				if calls := fake.Calls(method); len(calls) != 0 {
					t.Fatalf("the daemon received %s: %v", method, fake.Methods())
				}
			}
			if desktop != nil {
				for _, method := range desktop.Methods() {
					if method == "thread/resume" || method == "turn/start" {
						t.Fatalf("Desktop received %s", method)
					}
				}
			}
		})
	}
}

type codexWrite struct {
	name  string
	write func(*Codex, *surface.Session) error
}

func codexWrites() []codexWrite {
	ctx := context.Background()
	return []codexWrite{
		{name: "send", write: func(c *Codex, s *surface.Session) error { _, err := c.Send(ctx, s, "hello"); return err }},
		{name: "steer", write: func(c *Codex, s *surface.Session) error { return c.Steer(ctx, s, "focus") }},
		{name: "interrupt", write: func(c *Codex, s *surface.Session) error { return c.Interrupt(ctx, s) }},
		{name: "compact", write: func(c *Codex, s *surface.Session) error { return c.Compact(ctx, s) }},
		{name: "goal clear", write: func(c *Codex, s *surface.Session) error { return c.GoalClear(ctx, s) }},
		{name: "model", write: func(c *Codex, s *surface.Session) error { _, err := c.Model(ctx, s, "gpt-5.6-sol"); return err }},
		{name: "fork", write: func(c *Codex, s *surface.Session) error {
			_, err := c.ForkSession(ctx, s, surface.ForkOptions{})
			return err
		}},
		{name: "queue add", write: func(c *Codex, s *surface.Session) error {
			_, err := c.NativeQueue(ctx, s, surface.NativeQueueRequest{Action: "add", Message: "later", ClientID: "client-1"})
			return err
		}},
		{name: "queue delete", write: func(c *Codex, s *surface.Session) error {
			_, err := c.NativeQueue(ctx, s, surface.NativeQueueRequest{Action: "delete", ID: "queued-1"})
			return err
		}},
	}
}

var codexOperationMethods = []string{"turn/start", "turn/steer", "turn/interrupt", "thread/compact/start", "thread/goal/clear", "thread/fork", "thread/queue/add", "thread/queue/delete"}

func requireNoCodexOperation(t *testing.T, fake *codextest.Daemon) {
	t.Helper()
	for _, method := range codexOperationMethods {
		if calls := fake.Calls(method); len(calls) != 0 {
			t.Fatalf("%s ran: %v", method, fake.Methods())
		}
	}
}

func requireOwnershipConflict(t *testing.T, err error) {
	t.Helper()
	var terminal surface.DeliveryTerminalError
	if !errors.As(err, &terminal) || terminal.Kind != surface.DeliveryOwnershipConflict || surface.IsDeliveryUnavailable(err) || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatalf("err=%v, want only a terminal ownership conflict", err)
	}
}

func writerConflict(method string, _ map[string]any) map[string]any {
	if method == "thread/resume" {
		return map[string]any{"error": map[string]any{"code": -32600, "message": "thread " + managedThreadID + " already has an active writer"}}
	}
	return nil
}

func TestCodexWriterConflictIsTerminalForEveryWrite(t *testing.T) {
	for _, test := range codexWrites() {
		t.Run(test.name, func(t *testing.T) {
			fake := startManagedCodex(t, writerConflict)
			fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Turns: []codextest.Turn{{ID: "turn-1", Status: "inProgress"}}})
			err := test.write(isolatedManagedRuntime(t), &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop})
			requireOwnershipConflict(t, err)
			requireNoCodexOperation(t, fake)
		})
	}
}

func TestCodexModelChangeWriterConflictIsTerminal(t *testing.T) {
	fake := startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		if params["model"] != nil {
			return writerConflict(method, params)
		}
		return nil
	})
	fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail"})
	_, err := isolatedManagedRuntime(t).Model(context.Background(), &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}, "gpt-5.6-sol")
	requireOwnershipConflict(t, err)
	if resumes := fake.Calls("thread/resume"); len(resumes) != 2 || resumes[1]["model"] != "gpt-5.6-sol" {
		t.Fatalf("want the preflight resume, then the conflicting model change, got %v", resumes)
	}
}

func TestCodexResumedThreadWithoutDirectInputStaysRetryable(t *testing.T) {
	for _, test := range codexWrites() {
		t.Run(test.name, func(t *testing.T) {
			fake := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
				if method == "thread/read" {
					return map[string]any{"thread": map[string]any{"canAcceptDirectInput": false}}
				}
				return nil
			})
			fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Turns: []codextest.Turn{{ID: "turn-1", Status: "inProgress"}}})
			err := test.write(isolatedManagedRuntime(t), &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop})
			if !surface.IsDeliveryUnavailable(err) || surface.IsDeliveryTerminal(err) || surface.IsDeliveryOutcomeUnknown(err) {
				t.Fatalf("err=%v, want only retryable unavailable", err)
			}
			requireNoCodexOperation(t, fake)
		})
	}
}
