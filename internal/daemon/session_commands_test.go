package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type commandsSurface struct {
	*daemonSurface
	commands *surface.RuntimeCommands
	err      error
}

func (s *commandsSurface) Commands(context.Context, *surface.Session) (*surface.RuntimeCommands, error) {
	return s.commands, s.err
}

func serveSessionCommands(t *testing.T, d *Daemon, id string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session-commands?id="+id, nil)
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	response := httptest.NewRecorder()
	dashboardRouter(d).ServeHTTP(response, request)
	return response
}

func TestSessionCommandsAnswersWhatTheRuntimeRunsForAPerson(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	listed := &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{{Name: "review", Description: "Review", Insert: "$review"}}}
	response := serveSessionCommands(t, newTestDaemon(t, reg, []surface.Surface{&commandsSurface{daemonSurface: fake, commands: listed}}), from.ID)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"commands": []any{map[string]any{"name": "review", "description": "Review", "argumentHint": "", "insert": "$review"}}, "unavailable": ""}
	if got, _ := json.Marshal(body); string(got) != string(mustJSON(t, want)) {
		t.Fatalf("body = %s, want %s", got, mustJSON(t, want))
	}
}

func TestSessionCommandsIsEmptyForARuntimeWithoutCommandsAndFailsWhenTheRuntimeCannotList(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	response := serveSessionCommands(t, newTestDaemon(t, reg, []surface.Surface{fake}), from.ID)
	if response.Code != http.StatusOK || response.Body.String() != `{"commands":[],"unavailable":""}`+"\n" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	failing := &commandsSurface{daemonSurface: fake, err: errors.New("no answer")}
	response = serveSessionCommands(t, newTestDaemon(t, reg, []surface.Surface{failing}), from.ID)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s, want 502", response.Code, response.Body.String())
	}
	if response := serveSessionCommands(t, newTestDaemon(t, reg, []surface.Surface{fake}), "missing"); response.Code != http.StatusNotFound {
		t.Fatalf("missing session status=%d", response.Code)
	}
}

func TestASessionsCommandsAreOneJournalRowThatIsNoTimelineItem(t *testing.T) {
	d, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newTestSourceManager(t, reg), session: from, adapter: fake, epoch: "epoch", appendBodies: map[string]string{}}
	source.append(surface.StreamEvent{ID: "user-1", ProviderKey: "user-1", Kind: "message", Role: "user", Title: "user", Text: "hello"})
	first := &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{{Name: "review", Description: "Review", ArgumentHint: "[pr]", Insert: "/review"}}}
	source.append(surface.StreamEvent{ID: "commands", ProviderKey: "commands", Version: 1, Operation: "replace", Kind: "commands", Commands: first})
	source.append(surface.StreamEvent{ID: "commands", ProviderKey: "commands", Version: 2, Operation: "replace", Kind: "commands", Commands: &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{}, Unavailable: "Commands run only when this session has Remote Control."}})
	page, err := d.readJournalPage(from.ID, 0, 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].Text != "hello" {
		t.Fatalf("page=%+v err=%v, want only the message", page, err)
	}
	var rows []map[string]any
	var cursor uint64
	if _, err := readSessionJournalAfter(reg, from.ID, &cursor, func(entry registry.SessionJournalEntry) bool {
		if entry.Kind == "commands" {
			var payload map[string]any
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, payload)
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("commands rows = %d, want one replaced in place", len(rows))
	}
	row := rows[0]
	if row["itemId"] != "state:commands" || row["op"] != "replace" || row["unavailable"] != "Commands run only when this session has Remote Control." || row["commands"] != nil || row["ts"] == "" {
		t.Fatalf("row = %+v", row)
	}
}

func TestAStreamThatStartsOverWithTheSameCommandsLeavesTheirRowAsItIs(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	source := &sessionSource{manager: newTestSourceManager(t, reg), session: from, adapter: fake, epoch: "epoch", appendBodies: map[string]string{}}
	listed := func() *surface.RuntimeCommands {
		return &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{{Name: "review", Description: "Review", Insert: "/review"}}}
	}
	source.append(surface.StreamEvent{ID: "commands", ProviderKey: "commands", Version: 1, Operation: "replace", Kind: "commands", Commands: listed()})
	stored, found, err := reg.SessionJournalEntryByProviderKey(from.ID, "state:commands")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	source.append(surface.StreamEvent{ID: "commands", ProviderKey: "commands", Version: 1, Operation: "replace", Kind: "commands", Commands: listed()})
	again, _, err := reg.SessionJournalEntryByProviderKey(from.ID, "state:commands")
	if err != nil || again.Seq != stored.Seq || string(again.Payload) != string(stored.Payload) {
		t.Fatalf("row seq %d -> %d, want the same row", stored.Seq, again.Seq)
	}
	source.append(surface.StreamEvent{ID: "commands", ProviderKey: "commands", Version: 1, Operation: "replace", Kind: "commands", Commands: &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{}, Unavailable: "gone"}})
	if changed, _, _ := reg.SessionJournalEntryByProviderKey(from.ID, "state:commands"); changed.Seq == stored.Seq {
		t.Fatal("a changed list left the row as it was")
	}
}
