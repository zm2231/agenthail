package daemon

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestACatalogPassLooksUpCodexDesktopOnceAndStillFindsIt(t *testing.T) {
	const thread = "019f004a-0000-7000-8000-0000000d0002"
	record := `{"id":"` + thread + `","originator":"Codex Desktop","source":"vscode","status":{"type":"idle"},"canAcceptDirectInput":true,"updatedAt":1760000000}`
	desktop := codextest.StartDesktop(t, func(method string) string {
		switch method {
		case "thread/loaded/list":
			return `{"result":{"data":["` + thread + `"]}}`
		case "thread/list":
			return `{"result":{"data":[` + record + `]}}`
		case "thread/read":
			return `{"result":{"thread":` + record + `}}`
		case "thread/turns/list":
			return `{"result":{"data":[]}}`
		}
		return ""
	})
	_, registry, _, _, _ := daemonFixture(t)
	d := newTestDaemon(t, registry, []surface.Surface{surfaces.NewCodex(desktop.URL)})
	for pass := 1; pass <= 2; pass++ {
		d.discoverCatalog(context.Background())
		if lookups := desktop.Lookups(); lookups != pass {
			t.Fatalf("after %d catalog passes Desktop was looked up %d times", pass, lookups)
		}
	}
	if session, err := registry.Session(thread); err != nil || session == nil || session.RuntimeKind != surface.RuntimeCodexDesktop {
		t.Fatalf("the catalog did not find the Desktop thread: session=%+v err=%v", session, err)
	}
	health, _ := d.observedSurfaceHealth(surface.KindCodex)
	if health.Health != "healthy" || health.Runtime == nil || !health.Runtime.Reachable {
		t.Fatalf("health=%+v runtime=%+v", health, health.Runtime)
	}
}
