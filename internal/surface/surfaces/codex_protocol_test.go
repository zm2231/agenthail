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

func startDesktopBridge(t *testing.T, respond func(method string) string) *codextest.Desktop {
	t.Helper()
	return codextest.StartDesktop(t, respond)
}

func startRendererDesktopBridge(t *testing.T) string {
	t.Helper()
	return startDesktopBridge(t, func(method string) string {
		switch method {
		case "thread/read":
			return `{"result":{"thread":{"id":"thread","source":"vscode","status":{"type":"idle"}}}}`
		case "thread/loaded/list", "thread/items/list":
			return `{"result":{"data":[]}}`
		case "thread/turns/list":
			return `{"result":{"data":[{"id":"completed","status":{"type":"completed"}}]}}`
		case "thread/start":
			return `{"result":{"cwd":"/tmp/project","thread":{"id":"desktop-new","name":"Desktop conversation","source":"vscode"}}}`
		case "turn/start":
			return `{"result":{"turn":{"id":"desktop-turn"},"method":"turn/start"}}`
		}
		return ""
	}).URL
}

func TestCodexStartSessionPrefersDesktopOwner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bridge := startDesktopBridge(t, func(method string) string {
		switch method {
		case "thread/start":
			return `{"result":{"cwd":"/tmp/project","thread":{"id":"desktop-new","name":"","source":"vscode"}}}`
		case "turn/start":
			return `{"result":{"turn":{"id":"desktop-turn"}}}`
		}
		return ""
	})
	session, sent, err := NewCodex(bridge.URL).StartSession(context.Background(), surface.SessionStartOptions{Message: "Build the release", Cwd: "/tmp/project", Model: "gpt-5.6-sol", ApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "desktop-new" || session.Transport != codexTransportDesktop || session.Status != surface.StatusBusy || session.Name != "Build the release" || sent.UUID != "desktop-turn" || !sent.Accepted {
		t.Fatalf("session=%+v sent=%+v", session, sent)
	}
	for _, field := range []string{`"threadSource":"agenthail"`, `"serviceName":"agenthail"`, `"cwd":"/tmp/project"`, `"model":"gpt-5.6-sol"`, `"approvalPolicy":"on-request"`} {
		if !strings.Contains(bridge.Request("thread/start"), field) {
			t.Fatalf("thread/start missing %s: %s", field, bridge.Request("thread/start"))
		}
	}
	if !strings.Contains(bridge.Request("turn/start"), `"threadId":"desktop-new"`) {
		t.Fatalf("turn/start not bound to the created thread: %s", bridge.Request("turn/start"))
	}
}

func TestCodexDesktopDiscoveryNeverBootstrapsManagedRuntime(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "managed-runtime.log")
	codexHome := filepath.Join(root, "codex-home")
	installStandaloneCodex(t, codexHome, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AGENTHAIL_TEST_LOG\"\nexit 1\n")
	t.Setenv("AGENTHAIL_TEST_LOG", logPath)
	t.Setenv("CODEX_HOME", codexHome)
	if _, err := NewCodex(startRendererDesktopBridge(t)).List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if output, _ := os.ReadFile(logPath); len(output) != 0 {
		t.Fatalf("Desktop discovery bootstrapped managed runtime: %s", output)
	}
}

func TestCodexResolveExactNameUsesHistorySearch(t *testing.T) {
	bridge := startDesktopBridge(t, func(method string) string {
		if method == "thread/search" {
			return `{"result":{"data":[{"thread":{"id":"thread-1","name":"Q","cwd":"/tmp","source":"vscode","status":{"type":"idle"}},"snippet":"test"}]}}`
		}
		return ""
	})
	session, err := NewCodex(bridge.URL).Resolve(context.Background(), "Q")
	if err != nil || session == nil || session.ID != "thread-1" || session.Transport != codexTransportDesktop || countMethod(bridge.Methods(), "thread/search") != 1 {
		t.Fatalf("session=%+v methods=%v err=%v", session, bridge.Methods(), err)
	}
}

func TestCodexResolveIDReadsThreadWithoutListing(t *testing.T) {
	const id = "019f004a-a94e-7313-a599-2db587a1f67a"
	bridge := startDesktopBridge(t, func(method string) string {
		switch method {
		case "thread/loaded/list":
			return `{"result":{"data":[]}}`
		case "thread/read":
			return `{"result":{"thread":{"id":"` + id + `","name":"known","cwd":"/tmp","source":"vscode","status":{"type":"idle"}}}}`
		}
		return ""
	})
	session, err := NewCodex(bridge.URL).Resolve(context.Background(), id)
	methods := bridge.Methods()
	if err != nil || session == nil || session.ID != id || session.Transport != codexTransportDesktop || countMethod(methods, "thread/read") != 1 || countMethod(methods, "thread/list") != 0 {
		t.Fatalf("session=%+v methods=%v err=%v", session, methods, err)
	}
}

func countMethod(methods []string, method string) int {
	count := 0
	for _, seen := range methods {
		if seen == method {
			count++
		}
	}
	return count
}

func TestCodexEnsureWritableRefreshesDesktopSourceTransport(t *testing.T) {
	codex := NewCodex(startRendererDesktopBridge(t))
	session := &surface.Session{ID: "thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "agenthail", Transport: codexTransportReadOnly}
	if err := codex.EnsureWritable(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Transport != codexTransportDesktop {
		t.Fatalf("transport=%q", session.Transport)
	}
}

func TestCodexDesktopSendNeverResumesLoadedThreadAndLoadsUnloadedThreadOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ready := `{"result":{"thread":{"id":"thread","source":"vscode","status":{"type":"idle"},"canAcceptDirectInput":true,"model":"gpt-5.6-terra"}}}`
	for _, test := range []struct {
		name       string
		firstRead  string
		session    surface.Session
		wantResume int
	}{
		{
			name:      "loaded thread behind stale managed transport",
			firstRead: ready,
			session:   surface.Session{ID: "thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Source: "agenthail", Transport: codexTransportManaged},
		},
		{
			name:       "unloaded Desktop thread",
			firstRead:  `{"result":{"thread":{"id":"thread","source":"vscode","status":{"type":"notLoaded"}}}}`,
			session:    surface.Session{ID: "thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "vscode", Transport: codexTransportDesktop},
			wantResume: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resumed := false
			bridge := startDesktopBridge(t, func(method string) string {
				switch method {
				case "thread/read":
					if resumed || test.wantResume == 0 {
						return ready
					}
					return test.firstRead
				case "thread/resume":
					if test.wantResume == 0 {
						return `{"error":{"code":-1,"message":"must not resume a loaded Desktop thread"}}`
					}
					resumed = true
					return `{"result":{"thread":{"id":"thread"}}}`
				case "thread/loaded/list":
					if test.wantResume == 0 {
						return `{"result":{"data":["thread"]}}`
					}
					return `{"result":{"data":[]}}`
				case "thread/turns/list":
					return `{"result":{"data":[]}}`
				case "turn/start":
					return `{"result":{"turn":{"id":"turn"}}}`
				}
				return ""
			})
			session := test.session
			sent, err := NewCodex(bridge.URL).SendWithOptions(context.Background(), &session, "deliver queued work", surface.SendOptions{TurnOptions: surface.TurnOptions{Mode: "plan"}})
			if err != nil || sent == nil || !sent.Accepted || sent.UUID != "turn" {
				t.Fatalf("sent=%+v err=%v", sent, err)
			}
			if session.Transport != codexTransportDesktop {
				t.Fatalf("session=%+v", session)
			}
			methods := bridge.Methods()
			resumes, starts, resumeAt, startAt := 0, 0, -1, -1
			for index, method := range methods {
				switch method {
				case "thread/resume":
					resumes++
					resumeAt = index
				case "turn/start":
					starts++
					startAt = index
				}
			}
			if resumes != test.wantResume || starts != 1 || resumeAt > startAt {
				t.Fatalf("methods=%v", methods)
			}
		})
	}
}

func TestCodexObserveReadsThroughDesktopWhenTheDaemonIsStopped(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	codex := NewCodex(startRendererDesktopBridge(t))
	session := &surface.Session{ID: "thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Source: "agenthail", Transport: codexTransportManaged}
	observation, err := codex.Observe(context.Background(), session)
	if err != nil || observation == nil || observation.Status != surface.StatusIdle {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
}

func TestCodexResolveRejectsDuplicateExactNamesAcrossPages(t *testing.T) {
	bridge := startDesktopBridge(t, func(method string) string {
		if method == "thread/search" {
			return `{"result":{"data":[{"thread":{"id":"thread-1","name":"duplicate","cwd":"/one"}},{"thread":{"id":"thread-2","name":"DUPLICATE","cwd":"/two"}}]}}`
		}
		return ""
	})
	session, err := NewCodex(bridge.URL).Resolve(context.Background(), "duplicate")
	if err == nil || session != nil || !strings.Contains(err.Error(), "thread-1") || !strings.Contains(err.Error(), "thread-2") {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}
