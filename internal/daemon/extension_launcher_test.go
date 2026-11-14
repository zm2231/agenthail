package daemon

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/extension"
)

func changedIDs(change extension.Change) []string {
	var ids []string
	for _, files := range change.Extensions {
		ids = append(ids, files.ID)
	}
	return ids
}

func TestTheHistoryOfOneExtensionListsTheChangesThatTouchedItWhole(t *testing.T) {
	f := startExtensionFixture(t)
	changeOwnPage(t, f, "green", "turn-0")
	changeOtherExtension(t, f, "turn-1")
	writeExtension(t, extension.Workspace(f.home), "notes", `{"label": "Notes, renamed"}`)
	if err := os.WriteFile(filepath.Join(f.workdir, "assets", "app.css"), []byte("body { color: blue }"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.turn(t, "to", "turn-2")

	notes := f.historyOf(t, "notes")
	if len(notes) != 2 || notes[0].TurnID != "turn-2" || notes[1].TurnID != "turn-1" {
		t.Fatalf("notes' history = %+v, want turn-2 then turn-1", notes)
	}
	if ids := changedIDs(notes[0]); !slices.Equal(ids, []string{"fixture", "notes"}) {
		t.Fatalf("a change that also touched fixture lists %v; undoing it reverts both", ids)
	}
	for _, change := range f.historyOf(t, "fixture") {
		if change.TurnID == "turn-1" {
			t.Fatalf("fixture's history lists a change only to notes: %+v", change)
		}
	}
	if response, body := f.request(t, http.MethodGet, "/api/v1/extensions/history?id=..%2Fnotes", "secret", nil); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("a path for an id answered %d %s", response.StatusCode, body)
	}
}

func clickIn(t *testing.T, agent *mcp.ClientSession, tab, selector, name string) {
	t.Helper()
	snapshot := browse(t, agent, map[string]any{"action": "snapshot", "tab": tab, "selector": selector, "interactive": true})["snapshot"].(string)
	ref := regexp.MustCompile(`button "` + regexp.QuoteMeta(name) + `[^"]*" \[ref=(\w+)\]`).FindStringSubmatch(snapshot)
	if ref == nil {
		t.Fatalf("no %q button in %s: %s", name, selector, snapshot)
	}
	browse(t, agent, map[string]any{"action": "act", "tab": tab, "kind": "click", "ref": ref[1]})
}

func TestTheExtensionsTabLaunchesEachExtensionAndShowsItsOwnHistory(t *testing.T) {
	f := startExtensionFixtureWith(t, map[string]string{"ui.html": stampedPage})
	agent := browserAgent(t, f)
	browser := signIn(t, f, agent)
	changeOwnPage(t, f, "green", "turn-0")
	changeOtherExtension(t, f, "turn-1")

	tab := browse(t, agent, map[string]any{"action": "open", "url": browser.URL + "/#extensions"})["tab"].(string)
	text := func(selector string) string {
		out, _ := callMCP(t, agent, "agenthail_browser", map[string]any{"action": "text", "tab": tab, "selector": selector})
		got, _ := out["text"].(string)
		return got
	}
	eventually(t, "the dashboard frames the first page", func() bool { return browser.loads() > 0 })
	eventually(t, "the rail lists the page and the extension without one", func() bool {
		rail := text("#extensions-list")
		return strings.Contains(rail, "Fixture") && strings.Contains(rail, "Notes")
	})

	clickIn(t, agent, tab, "#extensions-toolbar", "History")
	eventually(t, "the history shows the page's own change and not the other extension's", func() bool {
		history := text("#extensions-history")
		return strings.Contains(history, "changed 1 file in fixture") && !strings.Contains(history, "created notes")
	})

	clickIn(t, agent, tab, "#extensions-list", "Notes")
	eventually(t, "the history follows the selected extension", func() bool {
		history := text("#extensions-history")
		return strings.Contains(history, "History of Notes") && strings.Contains(history, "created notes") && !strings.Contains(history, "in fixture")
	})
}
