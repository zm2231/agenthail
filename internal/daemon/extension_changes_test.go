package daemon

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
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

func asksNothing(t *testing.T, reg *registry.Registry, sessionID string) {
	t.Helper()
	asks, err := reg.SessionAsks(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ask := range asks {
		if ask.State == registry.AskOpen {
			t.Fatalf("a change to extensions left %s waiting on the person: %+v", sessionID, ask)
		}
	}
}

func (f *extensionFixture) handle(t *testing.T, events ...extensionTurnEvent) {
	t.Helper()
	done := make(chan struct{})
	events[len(events)-1].done = done
	for _, event := range events {
		f.d.queueExtensionTurn(event)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the turn events were not handled")
	}
	f.d.background.wait()
}

func (f *extensionFixture) turn(t *testing.T, sessionID, turnID string) {
	t.Helper()
	f.handle(t, extensionTurnEvent{kind: extensionTurnEnd, sessionID: sessionID, turnID: turnID})
}

func (f *extensionFixture) start(t *testing.T, sessionID string) {
	t.Helper()
	f.handle(t, extensionTurnEvent{kind: extensionTurnStart, sessionID: sessionID})
}

func (f *extensionFixture) changes(t *testing.T) []extension.Change {
	t.Helper()
	return f.historyOf(t, "")
}

func (f *extensionFixture) historyOf(t *testing.T, id string) []extension.Change {
	t.Helper()
	path := "/api/v1/extensions/history"
	if id != "" {
		path += "?id=" + id
	}
	response, body := f.request(t, http.MethodGet, path, "secret", nil)
	var view struct {
		Changes []extension.Change `json:"changes"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(body, &view) != nil {
		t.Fatalf("history answered %d %s", response.StatusCode, body)
	}
	return view.Changes
}

func (f *extensionFixture) undo(t *testing.T, rev string) (int, []byte) {
	t.Helper()
	response, body := f.request(t, http.MethodPost, "/api/v1/extensions", "secret", map[string]string{"action": "undo", "rev": rev})
	return response.StatusCode, body
}

func TestUndoingTheTurnThatCreatedAnExtensionRemovesItsTablesAndPortsWhileALaterExtensionsTablesSurvive(t *testing.T) {
	f := startExtensionFixture(t)
	reg := f.d.Registry
	previous := extensionPortBase
	extensionPortBase = 0
	t.Cleanup(func() { extensionPortBase = previous })
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
	asksNothing(t, reg, "to")

	writeExtension(t, workspace, "notes-extra", `{"label": "Notes extra"}`)
	f.turn(t, "to", "turn-2")
	extra := f.changes(t)[0]
	asksNothing(t, reg, "to")
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
	if status, body := f.undo(t, extra.Rev); status != http.StatusOK {
		t.Fatalf("undoing notes-extra's creation answered %d %s", status, body)
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
	unrelated := filepath.Join(f.workdir, "assets", "app.css")
	if err := os.WriteFile(unrelated, []byte("p {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, body := f.undo(t, first.Rev); status != http.StatusConflict || !strings.Contains(string(body), "later change") {
		t.Fatalf("undoing a change a later one edited answered %d %s", status, body)
	}
	if data, _ := os.ReadFile(page); string(data) != "<p>second</p>\n" {
		t.Fatalf("a refused undo changed the page to %q", data)
	}
	if data, _ := os.ReadFile(unrelated); string(data) != "p {}" {
		t.Fatalf("a refused undo lost an edit no turn had recorded yet: %q", data)
	}
	latest := f.changes(t)[0]
	write("<p>third, still being written</p>\n")
	if status, body := f.undo(t, latest.Rev); status != http.StatusConflict || !strings.Contains(string(body), "not recorded") {
		t.Fatalf("undoing a change whose files have unrecorded edits answered %d %s", status, body)
	}
	if data, _ := os.ReadFile(page); string(data) != "<p>third, still being written</p>\n" {
		t.Fatalf("a refused undo touched an unrecorded edit: %q", data)
	}
	write("<p>second</p>\n")
	if err := os.WriteFile(unrelated, []byte(`body { color: red }`), 0o600); err != nil {
		t.Fatal(err)
	}

	asksNothing(t, reg, "to")

	write("<p>by hand</p>\n")
	f.start(t, "from")
	f.turn(t, "from", "turn-3")
	latest = f.changes(t)[0]
	if latest.Author != extension.Person || latest.SessionID != "" {
		t.Fatalf("an edit made before the turn was recorded as %+v", latest)
	}
	asksNothing(t, reg, "from")
}

func TestTurnsAreRecordedInOrderAndATurnRunningElsewhereKeepsEditsFromBeingTakenForThePersons(t *testing.T) {
	f := startExtensionFixture(t)
	reg := f.d.Registry
	page := filepath.Join(f.workdir, "ui.html")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(page, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("<p>by the turn</p>\n")
	f.handle(t,
		extensionTurnEvent{kind: extensionTurnEnd, sessionID: "to", turnID: "turn-1"},
		extensionTurnEvent{kind: extensionTurnStart, sessionID: "to"},
	)
	if latest := f.changes(t)[0]; latest.SessionID != "to" || latest.TurnID != "turn-1" {
		t.Fatalf("a turn's edits were recorded as %+v when its next turn started at once", latest)
	}

	f.start(t, "from")
	write("<p>while two turns run</p>\n")
	f.start(t, "third")
	if latest := f.changes(t)[0]; latest.TurnID != "turn-1" {
		t.Fatalf("a turn starting while others ran recorded their edits as %+v", latest)
	}
	f.turn(t, "to", "turn-2")
	shared := f.changes(t)[0]
	if shared.SessionID != "to" || !slices.Equal(shared.AlsoRunning, []string{"from", "third"}) {
		t.Fatalf("the change made while others were mid-turn was recorded as %+v", shared)
	}
	asksNothing(t, reg, "to")
}

func raiseLeftoverAsks(t *testing.T, reg *registry.Registry) (change, grant registry.Ask) {
	t.Helper()
	change, err := reg.RaiseAsk(registry.Ask{SessionID: "to", Origin: registry.AskOriginExtensionChange, Title: "to changed extensions", Questions: []registry.AskQuestion{{Question: "Keep this change?", Options: []registry.AskOption{{ID: "keep", Label: "Keep"}, {ID: "undo", Label: "Undo"}}}}, RoutedTo: registry.AskToPerson}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	grant, err = reg.RaiseAsk(registry.Ask{SessionID: "to", Origin: registry.AskOriginExtensionGrant, Title: "approve notes", Questions: []registry.AskQuestion{{Question: "Approve?", Options: []registry.AskOption{{ID: "approve", Label: "Approve"}, {ID: "decline", Label: "Decline"}}}}, RoutedTo: registry.AskToPerson}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return change, grant
}

func askState(t *testing.T, reg *registry.Registry, id string) string {
	t.Helper()
	ask, _, err := reg.Ask(id)
	if err != nil {
		t.Fatal(err)
	}
	return ask.State
}

func TestChangeAsksLeftFromBeforeAreWithdrawnWhenTheDaemonStartsEvenWithExtensionsOff(t *testing.T) {
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RegisterSession(surface.Session{ID: "to", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	change, grant := raiseLeftoverAsks(t, store)
	runDaemon(t, store)
	testkit.Eventually(t, "the change ask withdrawn at start", 5*time.Second, func() bool { return askState(t, store, change.ID) == registry.AskWithdrawn })
	if state := askState(t, store, grant.ID); state != registry.AskOpen {
		t.Fatalf("an approval the person still has to give was %s", state)
	}
}

func TestAnsweringAChangeAskLeftFromBeforeIsStaleAndWithdrawsIt(t *testing.T) {
	f := startExtensionFixture(t)
	change, _ := raiseLeftoverAsks(t, f.d.Registry)
	if status := f.answer(t, change, "keep"); status != http.StatusConflict {
		t.Fatalf("keeping a change ask left from before answered %d, want 409", status)
	}
	if state := askState(t, f.d.Registry, change.ID); state != registry.AskWithdrawn {
		t.Fatalf("the answered change ask is %s", state)
	}
}

func TestTurnEventsQueuedAtShutdownAreHandledBeforeTheWorkerReturns(t *testing.T) {
	f := startExtensionFixture(t)
	c := f.d.extensions.changes
	c.mu.Lock()
	f.d.queueExtensionTurn(extensionTurnEvent{kind: extensionTurnEnd, sessionID: "busy", turnID: "turn-0"})
	time.Sleep(50 * time.Millisecond)
	f.d.queueExtensionTurn(extensionTurnEvent{kind: extensionTurnStart, sessionID: "to"})
	stopped := make(chan struct{})
	go func() {
		f.stop()
		close(stopped)
	}()
	time.Sleep(50 * time.Millisecond)
	c.mu.Unlock()
	select {
	case <-stopped:
	case <-time.After(30 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if !c.inFlight["to"] {
		t.Fatal("a turn start queued at shutdown was dropped, so a later start would take that turn's edits for the person's")
	}
}
