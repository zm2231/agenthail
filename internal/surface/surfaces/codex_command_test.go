package surfaces

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

func busyDesktopBridge(t *testing.T) string {
	t.Helper()
	return startDesktopBridge(t, func(method string) string {
		switch method {
		case "thread/read":
			return `{"result":{"thread":{"id":"thread","source":"vscode","status":{"type":"active"},"canAcceptDirectInput":true}}}`
		case "thread/loaded/list":
			return `{"result":{"data":["thread"]}}`
		case "thread/turns/list":
			return `{"result":{"data":[{"id":"running","status":"inProgress"}]}}`
		case "turn/steer":
			return `{"result":{}}`
		}
		return ""
	}).URL
}

func assertComposerCommandRefused(t *testing.T, err error, name string) {
	t.Helper()
	if !surface.IsDeliveryTerminal(err) || surface.DeliveryTerminalReason(err) != surface.DeliveryInvalidRequest {
		t.Fatalf("err=%v, want a terminal invalid-request refusal", err)
	}
	if !strings.Contains(err.Error(), "/"+name) || !strings.Contains(err.Error(), "$"+name) {
		t.Fatalf("refusal does not name the command and the skill form: %v", err)
	}
}

func idleDesktopBridge(t *testing.T) *codextest.Desktop {
	t.Helper()
	return startDesktopBridge(t, func(method string) string {
		switch method {
		case "thread/read":
			return `{"result":{"thread":{"id":"thread","source":"vscode","status":{"type":"idle"},"canAcceptDirectInput":true}}}`
		case "thread/loaded/list":
			return `{"result":{"data":["thread"]}}`
		case "thread/turns/list":
			return `{"result":{"data":[]}}`
		case "thread/start":
			return `{"result":{"cwd":"/tmp/project","thread":{"id":"desktop-new","source":"vscode"}}}`
		case "turn/start":
			return `{"result":{"turn":{"id":"desktop-turn"}}}`
		}
		return ""
	})
}

func TestCodexRefusesAComposerCommandBeforeAnyTurnStarts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	session := &surface.Session{ID: "thread", Surface: surface.KindCodex, Transport: codexTransportDesktop, Source: "vscode"}
	bridge := idleDesktopBridge(t)
	codex := NewCodex(bridge.URL)
	for _, message := range []string{"/l5-probe", "/l5-probe with arguments", "/status"} {
		_, err := codex.SendWithOptions(context.Background(), session, message, surface.SendOptions{})
		assertComposerCommandRefused(t, err, strings.Fields(message)[0][1:])
	}
	_, _, err := codex.StartSession(context.Background(), surface.SessionStartOptions{Message: "  /l5-probe", Cwd: "/tmp/project"})
	assertComposerCommandRefused(t, err, "l5-probe")
	if methods := bridge.Methods(); countMethod(methods, "turn/start") != 0 || countMethod(methods, "thread/start") != 0 {
		t.Fatalf("a composer command reached Codex: %v", methods)
	}

	for _, message := range []string{"/tmp/build.log shows the failure", " /status as words", "/", "$l5-probe", "run /l5-probe"} {
		bridge := idleDesktopBridge(t)
		sent, err := NewCodex(bridge.URL).SendWithOptions(context.Background(), session, message, surface.SendOptions{})
		if err != nil || sent == nil || !sent.Accepted {
			t.Fatalf("prompt %q: sent=%+v err=%v", message, sent, err)
		}
		text, _ := json.Marshal(message)
		if request := bridge.Request("turn/start"); !strings.Contains(request, `"text":`+string(text)) {
			t.Fatalf("prompt %q not sent verbatim: %s", message, request)
		}
	}
}

func TestCodexRefusesAComposerCommandAsASteer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	codex := NewCodex(busyDesktopBridge(t))
	session := &surface.Session{ID: "thread", Surface: surface.KindCodex, Transport: codexTransportDesktop, Source: "vscode"}
	assertComposerCommandRefused(t, codex.Steer(context.Background(), session, "/l5-probe"), "l5-probe")
	if err := codex.Steer(context.Background(), session, "/tmp/build.log too"); err != nil {
		t.Fatalf("a path steer was refused: %v", err)
	}
}
