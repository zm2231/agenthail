package surfaces

import (
	"context"
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
	session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex, Transport: codexTransportManaged}
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

func TestCodexDesktopCreatedThreadIsNeverLoadedIntoTheDaemon(t *testing.T) {
	fake := startManagedCodex(t, nil)
	fake.AddThread(codextest.Thread{ID: "desktop-thread", Originator: codexDesktopOriginator})
	codex := isolatedManagedRuntime(t)
	session := &surface.Session{ID: "desktop-thread", Surface: surface.KindCodex, Transport: codexTransportManaged}
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
			_, err := isolatedManagedRuntime(t).Send(context.Background(), &surface.Session{ID: managedThreadID, Surface: surface.KindCodex}, "hello")
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
			session := &surface.Session{ID: managedThreadID, Surface: surface.KindCodex}
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
