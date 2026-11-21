package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

func TestACodexDesktopThreadKeepsItsRowUntilDesktopQuits(t *testing.T) {
	const threadID = "019f004a-0000-7000-8000-0000000000d2"
	thread := map[string]any{"id": threadID, "name": "from Desktop", "originator": "Codex Desktop", "source": "vscode", "createdAt": 1795000000, "updatedAt": 1795000000, "status": map[string]any{"type": "notLoaded"}}
	codextest.Start(t, func(method string, _ map[string]any) map[string]any {
		switch method {
		case "thread/loaded/list":
			return map[string]any{"data": []any{}}
		case "thread/list":
			return map[string]any{"data": []any{thread}}
		}
		return nil
	})
	listed, err := json.Marshal(map[string]any{"result": map[string]any{"data": []any{thread}}})
	if err != nil {
		t.Fatal(err)
	}
	desktop := codextest.StartDesktop(t, func(method string) string {
		switch method {
		case "thread/loaded/list":
			return `{"result":{"data":[]}}`
		case "thread/list":
			return string(listed)
		}
		return ""
	})
	var bridged atomic.Bool
	bridged.Store(true)
	debugPort := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !bridged.Load() {
			_, _ = w.Write([]byte("[]"))
			return
		}
		response, err := http.Get(desktop.URL + "/json")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		_, _ = io.Copy(w, response.Body)
	}))
	t.Cleanup(debugPort.Close)
	endpoint, err := url.Parse(debugPort.URL)
	if err != nil {
		t.Fatal(err)
	}
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	d := newTestDaemon(t, store, []surface.Surface{codexsurface.NewCodex(endpoint.Port())})
	pass := func() []map[string]any {
		t.Helper()
		_, before, err := store.CatalogState()
		if err != nil {
			t.Fatal(err)
		}
		d.discoverCatalog(context.Background())
		window, err := store.CatalogEventsAfter(before, 1000)
		if err != nil {
			t.Fatal(err)
		}
		var rows []map[string]any
		for _, event := range window.Events {
			if event.Type != "session.upserted" || event.EntityID != threadID {
				continue
			}
			var envelope struct {
				Session map[string]any `json:"session"`
			}
			if err := json.Unmarshal(event.Payload, &envelope); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, envelope.Session)
		}
		return rows
	}
	if rows := pass(); len(rows) != 1 || rows[0]["readOnly"] == true || rows[0]["transport"] != "desktop" {
		t.Fatalf("with Desktop reachable the thread published %v", rows)
	}
	bridged.Store(false)
	if rows := pass(); len(rows) != 0 {
		t.Fatalf("a pass that could not reach a running Desktop republished the thread: %v", rows)
	}
	debugPort.Close()
	if rows := pass(); len(rows) != 1 || rows[0]["readOnly"] != true {
		t.Fatalf("after Desktop quit the thread published %v, want it read-only", rows)
	}
}
