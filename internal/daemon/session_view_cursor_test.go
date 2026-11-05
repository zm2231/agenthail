package daemon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestSessionViewKeysBreakTiesByIDAndSurviveTheirEncoding(t *testing.T) {
	at := time.Date(2026, 11, 2, 12, 0, 0, 123456789, time.FixedZone("x", -5*3600))
	rows := []dashboardSession{{ID: "b", LastActive: at}, {ID: "a", LastActive: at}, {ID: "c", LastActive: at.Add(-time.Nanosecond)}}
	plan := newSessionViewPlan(rows, "all", SessionViewFilters{}, at.Add(time.Hour))
	order := []string{}
	for _, family := range plan.view.Families {
		order = append(order, family.ID)
	}
	if fmt.Sprint(order) != "[a b c]" {
		t.Fatalf("order %v", order)
	}
	request := sessionViewRequest{Scope: "all"}
	cursor, ok := decodeSessionViewCursor(encodeSessionViewCursor(request, plan.view.Families[0].key))
	if !ok {
		t.Fatal("the cursor did not decode")
	}
	if rest := familiesAfter(plan.view.Families, &cursor.After); len(rest) != 2 || rest[0].ID != "b" {
		t.Fatalf("after a, the walk resumed at %+v", rest)
	}
	last, _ := decodeSessionViewCursor(encodeSessionViewCursor(request, plan.view.Families[2].key))
	if rest := familiesAfter(plan.view.Families, &last.After); len(rest) != 0 {
		t.Fatalf("past the last family, the walk returned %+v", rest)
	}
	activityCursor := base64.RawURLEncoding.EncodeToString([]byte(`{"scope":"all","filters":{},"after":{"activity":"2026-11-02T12:00:00Z","id":"a"}}`))
	for _, raw := range []string{"", "e30", "not-base64!", activityCursor} {
		if _, ok := decodeSessionViewCursor(raw); ok {
			t.Fatalf("cursor %q decoded", raw)
		}
	}
}

func TestSessionViewPagesSurviveASessionMovingMidWalk(t *testing.T) {
	d, store, _, _, _ := daemonFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	record := func(id string, lastActive time.Time) {
		t.Helper()
		row := dashboardSession{ID: id, Name: id, Surface: surface.KindCodex, Status: surface.StatusIdle, LastActive: lastActive}
		raw, _ := json.Marshal(row)
		session := surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: row.Status, LastActive: lastActive}
		if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: now, ProjectionFingerprint: string(raw)}, registry.CatalogEvent{DedupeKey: id + string(raw), Type: "session.upserted", EntityID: id, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 5 {
		record(fmt.Sprint("s", i), now.Add(-time.Hour-time.Duration(i)*time.Minute))
	}
	page := func(cursor string) SessionViewSnapshot {
		t.Helper()
		query := "scope=all&limit=2"
		if cursor != "" {
			query += "&cursor=" + url.QueryEscape(cursor)
		}
		w := httptest.NewRecorder()
		d.sessionViewHandler(w, httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?"+query, nil))
		var state SessionViewSnapshot
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &state) != nil {
			t.Fatalf("page %q: %d %s", cursor, w.Code, w.Body.String())
		}
		return state
	}
	ids := func(state SessionViewSnapshot) []string {
		out := []string{}
		for _, row := range state.Sessions {
			out = append(out, row.ID)
		}
		return out
	}

	first := page("")
	if got := fmt.Sprint(ids(first)); got != "[s0 s1]" || first.NextCursor == "" {
		t.Fatalf("first page %s cursor=%q", got, first.NextCursor)
	}
	_, seq, err := store.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	record("s4", now.Add(-30*time.Minute))

	second := page(first.NextCursor)
	if got := fmt.Sprint(ids(second)); got != "[s2 s3]" || second.NextCursor != "" {
		t.Fatalf("after s4 moved to the front, the walk continued with %s cursor=%q", got, second.NextCursor)
	}
	seen := map[string]int{}
	for _, id := range append(ids(first), ids(second)...) {
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("%s came back %d times across pages", id, n)
		}
	}
	window, err := store.CatalogEventsAfter(seq, 100)
	if err != nil {
		t.Fatal(err)
	}
	moved := 0
	for _, event := range window.Events {
		if event.EntityID == "s4" {
			moved++
		}
	}
	if seen["s4"] != 0 || moved != 1 {
		t.Fatalf("the moved session reached the walk %d times and the stream %d times", seen["s4"], moved)
	}
}
