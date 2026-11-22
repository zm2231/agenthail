package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

func TestAClaimOverMCPTellsTheHolderHowToLogWithItsTool(t *testing.T) {
	d, r, _ := ledgerDaemon(t)
	path := plainFolder(t)
	id := addFolderProject(t, d, path)
	if err := ledger.NewStore(r.WriteDB(), r.ReadDB()).Enable(ledgerKeyAt(t, r, path), ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, folderSession(t, r, "worker", path)))
	if out, refused := callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": "w1", "request": map[string]any{"action": "add", "text": "tidy the notes"}}); refused {
		t.Fatalf("add: %v", out)
	}
	board, err := d.ledgerStore().Board(registry.LedgerProjectKey(id))
	if err != nil {
		t.Fatal(err)
	}
	item := board.Ready()[0].ID
	claimed, refused := callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": "w2", "request": map[string]any{"action": "claim", "itemId": item}})
	guidance, _ := claimed["guidance"].(string)
	if refused || !strings.Contains(guidance, "Log to "+item) || !strings.Contains(guidance, `"action":"log"`) || strings.Contains(guidance, "agenthail ledger log") {
		t.Fatalf("claim over MCP = %v, want the log call for the tool", claimed)
	}
	if out, refused := callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": "w3", "request": map[string]any{"action": "log", "itemId": item, "text": "chose the plain index", "logType": "decision"}}); refused {
		t.Fatalf("the log the guidance describes was refused: %v", out)
	}
}

func TestASessionStartingWithWorkInHandIsToldWhatItHolds(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	path := plainFolder(t)
	addFolderProject(t, d, path)
	key := ledgerKeyAt(t, r, path)
	store := d.ledgerStore()
	if err := store.Enable(key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	folderSession(t, r, "worker", path)
	folderSession(t, r, "idle", path)
	added, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkAdd, Text: "tidy the notes"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	item := added.Item.ID
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkClaim, ItemID: item}, time.Now()); err != nil {
		t.Fatal(err)
	}
	held := func(session string, start bool) []ledger.WorkItem {
		body, _ := json.Marshal(ledgerCheckpointRequest{SessionID: session, Start: start})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/ledger/checkpoint", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request)
		var out ledgerCheckpointResponse
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("checkpoint for %s: %d %s", session, w.Code, w.Body)
		}
		return out.Held
	}
	if got := held("worker", true); len(got) != 1 || got[0].ID != item {
		t.Fatalf("worker holds %+v, want %s", got, item)
	}
	if got := held("worker", false); len(got) != 0 {
		t.Fatalf("a work write's checkpoint listed held work: %+v", got)
	}
	if got := held("idle", true); len(got) != 0 {
		t.Fatalf("a session holding nothing was told it holds %+v", got)
	}
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkSubmit, ItemID: item}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Work(ledger.WorkRequest{ProjectKey: key, SessionID: "worker", Action: ledger.WorkClose, ItemID: item, Reason: "obsolete"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := held("worker", true); len(got) != 0 {
		t.Fatalf("a closed item is still held: %+v", got)
	}
}
