package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/registry"
)

const tableServer = `
export default function (ah: any) {
  ah.db.exec("CREATE TABLE IF NOT EXISTS " + ah.table('items') + " (x TEXT)");
  ah.action('ping', () => 'pong');
}
`

func writeExtension(t *testing.T, workspace, id, manifest string) {
	t.Helper()
	dir := filepath.Join(workspace, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"extension.json": manifest, "server.ts": tableServer} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func storeTables(t *testing.T, home string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", extension.StorePath(home)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		names = append(names, name)
	}
	return names
}

func openChangeAsk(t *testing.T, reg *registry.Registry, sessionID string) (registry.Ask, bool) {
	t.Helper()
	asks, err := reg.SessionAsks(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ask := range asks {
		if ask.Origin == registry.AskOriginExtensionChange && ask.State == registry.AskOpen {
			return ask, true
		}
	}
	return registry.Ask{}, false
}

func (f *extensionFixture) turn(t *testing.T, sessionID, turnID string) {
	t.Helper()
	f.d.extensionTurnCompleted(context.Background(), sessionID, turnID)
}

func (f *extensionFixture) changes(t *testing.T) []extension.Change {
	t.Helper()
	response, body := f.request(t, http.MethodGet, "/api/v1/extensions/history", "secret", nil)
	var view struct {
		Changes []extension.Change `json:"changes"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &view) != nil {
		t.Fatalf("history answered %d %s", response.StatusCode, body)
	}
	return view.Changes
}

func (f *extensionFixture) answer(t *testing.T, ask registry.Ask, choice string) int {
	t.Helper()
	response, body := f.request(t, http.MethodPost, "/api/v1/actions", "secret", json.RawMessage(answerBody(ask, choice)))
	if response.StatusCode != http.StatusOK {
		t.Logf("answer %s: %s", choice, body)
	}
	return response.StatusCode
}

func (f *extensionFixture) undo(t *testing.T, rev string) (int, []byte) {
	t.Helper()
	response, body := f.request(t, http.MethodPost, "/api/v1/extensions", "secret", map[string]string{"action": "undo", "rev": rev})
	return response.StatusCode, body
}

func TestUndoingTheTurnThatCreatedAnExtensionRemovesItsTablesAndPortsWhileALaterExtensionsTablesSurvive(t *testing.T) {
	f := startExtensionFixture(t)
	reg := f.d.Registry
	previous := extensionMountPortBase
	extensionMountPortBase = 0
	t.Cleanup(func() { extensionMountPortBase = previous })
	t.Cleanup(f.d.extensions.mounts.close)
	workspace := extension.Workspace(f.home)

	writeExtension(t, workspace, "notes", `{"label": "Notes", "mounts": [{"name": "app", "label": "App", "upstream": "http://127.0.0.1:9"}]}`)
	f.turn(t, "to", "turn-1")
	created := f.changes(t)[0]
	if created.SessionID != "to" || created.TurnID != "turn-1" || len(created.Extensions) != 1 || !created.Extensions[0].Created || created.Extensions[0].ID != "notes" {
		t.Fatalf("the first turn was recorded as %+v", created)
	}
	if err := f.d.approveExtension("notes"); err != nil {
		t.Fatal(err)
	}
	if err := f.d.syncExtensionMounts(); err != nil {
		t.Fatal(err)
	}
	if ports, _ := extension.ReadMountPorts(f.home); ports["notes/app"].Port == 0 {
		t.Fatalf("the approved mount got no port: %v", ports)
	}
	ask, open := openChangeAsk(t, reg, "to")
	if !open || !strings.Contains(ask.Title, "changed extensions") || !strings.Contains(ask.Questions[0].Question, "created notes") {
		t.Fatalf("the first turn raised %+v (open=%v)", ask, open)
	}

	writeExtension(t, workspace, "notes-extra", `{"label": "Notes extra"}`)
	f.turn(t, "to", "turn-2")
	rolled, open := openChangeAsk(t, reg, "to")
	if !open || rolled.ID != ask.ID || !strings.Contains(rolled.Title, "2 turns") {
		t.Fatalf("the second turn did not roll into the same ask: %+v", rolled)
	}
	tables := storeTables(t, f.home)
	if !slices.Contains(tables, "notes_items") || !slices.Contains(tables, "notes_extra_items") {
		t.Fatalf("the extensions did not make their tables: %v", tables)
	}

	if status, body := f.undo(t, created.Rev); status != http.StatusOK {
		t.Fatalf("undoing the creation answered %d %s", status, body)
	}
	tables = storeTables(t, f.home)
	if slices.Contains(tables, "notes_items") || !slices.Contains(tables, "notes_extra_items") {
		t.Fatalf("after undoing notes the store holds %v", tables)
	}
	if _, err := os.Stat(filepath.Join(workspace, "notes")); !os.IsNotExist(err) {
		t.Fatalf("the notes folder survived its undo: %v", err)
	}
	if ports, _ := extension.ReadMountPorts(f.home); ports["notes/app"].Port != 0 {
		t.Fatalf("the undone mount kept its port: %v", ports)
	}
	if grant, _ := f.d.extensions.approvals.Granted("notes"); grant.Mounts != nil {
		t.Fatalf("the undone extension kept its approval: %+v", grant)
	}
	reworded, open := openChangeAsk(t, reg, "to")
	if !open || reworded.ID != ask.ID || strings.Contains(reworded.Title, "2 turns") {
		t.Fatalf("the ask still lists the undone turn: %+v", reworded)
	}

	if response := f.answer(t, reworded, extensionUndo); response != http.StatusOK {
		t.Fatalf("Undo last answered %d", response)
	}
	if _, open := openChangeAsk(t, reg, "to"); open {
		t.Fatal("the ask stayed open with nothing left to keep or undo")
	}
	if slices.Contains(storeTables(t, f.home), "notes_extra_items") {
		t.Fatal("undoing notes-extra's creation left its table")
	}
}

func TestAChangeCannotBeUndoneUnderALaterOneAndHandEditsAreNotPinnedOnATurn(t *testing.T) {
	f := startExtensionFixture(t)
	reg := f.d.Registry
	page := filepath.Join(f.workdir, "ui.html")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(page, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("<p>first</p>\n")
	f.turn(t, "to", "turn-1")
	first := f.changes(t)[0]
	write("<p>second</p>\n")
	f.turn(t, "to", "turn-2")
	if status, body := f.undo(t, first.Rev); status != http.StatusConflict || !strings.Contains(string(body), "later change") {
		t.Fatalf("undoing a change a later one edited answered %d %s", status, body)
	}
	if data, _ := os.ReadFile(page); string(data) != "<p>second</p>\n" {
		t.Fatalf("a refused undo changed the page to %q", data)
	}

	ask, open := openChangeAsk(t, reg, "to")
	if !open {
		t.Fatal("no ask for the two turns")
	}
	if response := f.answer(t, ask, extensionKeep); response != http.StatusOK {
		t.Fatalf("Keep all answered %d", response)
	}
	if _, open := openChangeAsk(t, reg, "to"); open {
		t.Fatal("the ask stayed open after Keep all")
	}

	write("<p>by hand</p>\n")
	f.d.extensionTurnStarted()
	f.turn(t, "from", "turn-3")
	latest := f.changes(t)[0]
	if latest.Author != extension.Person || latest.SessionID != "" {
		t.Fatalf("an edit made before the turn was recorded as %+v", latest)
	}
	if _, open := openChangeAsk(t, reg, "from"); open {
		t.Fatal("a turn that changed nothing raised an ask")
	}
}
