package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestASendQueuedForAnUnloadedDesktopThreadWithNoTurnsLoadsItAndIsDelivered(t *testing.T) {
	const thread = "019f004a-0000-7000-8000-0000000d0003"
	var mu sync.Mutex
	loaded := false
	record := func() string {
		status, accepts := "notLoaded", "false"
		if loaded {
			status, accepts = "idle", "true"
		}
		return `{"id":"` + thread + `","originator":"Codex Desktop","source":"vscode","status":{"type":"` + status + `"},"canAcceptDirectInput":` + accepts + `}`
	}
	desktop := codextest.StartDesktop(t, func(method string) string {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "thread/loaded/list":
			if loaded {
				return `{"result":{"data":["` + thread + `"]}}`
			}
			return `{"result":{"data":[]}}`
		case "thread/read":
			return `{"result":{"thread":` + record() + `}}`
		case "thread/turns/list", "thread/items/list":
			return `{"result":{"data":[]}}`
		case "thread/resume":
			loaded = true
			return `{"result":{"thread":` + record() + `}}`
		case "turn/start":
			return `{"result":{"turn":{"id":"desktop-turn"}}}`
		}
		return ""
	})
	_, r, _, _, _ := daemonFixture(t)
	if err := r.RegisterSession(surface.Session{ID: thread, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusNotLoaded, Source: "vscode", Transport: "desktop"}); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(thread, "deliver after Desktop loads it"); err != nil {
		t.Fatal(err)
	}

	newTestDaemon(t, r, []surface.Surface{surfaces.NewCodex(desktop.URL)}).scanAndRelay(context.Background())

	if !strings.Contains(desktop.Request("turn/start"), "deliver after Desktop loads it") || r.QueueCount(thread) != 0 {
		t.Fatalf("Desktop methods=%v pending=%d", desktop.Methods(), r.QueueCount(thread))
	}
}
