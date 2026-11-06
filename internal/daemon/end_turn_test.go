package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type endTurnFixture struct {
	d       *Daemon
	r       *registry.Registry
	fake    *daemonSurface
	desktop *desktopRecorder
	person  *mcp.ClientSession
	worker  *mcp.ClientSession
	from    surface.Session
	to      surface.Session
}

func newEndTurnFixture(t *testing.T) endTurnFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, to := daemonFixture(t)
	desktop := &desktopRecorder{}
	d.notifyDesktop = desktop.notify
	if err := r.RecordSessionOrigin(registry.OriginOf(to.ID, from.ID)); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	return endTurnFixture{d: d, r: r, fake: fake, desktop: desktop, person: mcpSession(t, server, issueMCPToken(t, d, from)), worker: mcpSession(t, server, issueMCPToken(t, d, to)), from: from, to: to}
}

func (f endTurnFixture) turn(t *testing.T, session surface.Session, id string, during func()) {
	t.Helper()
	f.fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
	f.d.observeSession(context.Background(), f.fake, &session)
	f.fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: id}
	f.d.observeSession(context.Background(), f.fake, &session)
	during()
	f.fake.observations[session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: id, Reply: &surface.ReplyResult{Text: "reply", Done: true}}
	f.d.observeSession(context.Background(), f.fake, &session)
}

func (f endTurnFixture) alert(t *testing.T, suffix string) {
	t.Helper()
	testkit.Eventually(t, "a desktop alert ending "+suffix, 5*time.Second, func() bool { return len(f.desktop.matching(suffix)) == 1 })
}

var deployAsk = map[string]any{"headline": "Deploy the fix?", "needs": "you", "asks": []any{map[string]any{"question": "Deploy it now?", "options": []any{map[string]any{"label": "Deploy"}, map[string]any{"label": "Wait", "does": "hold until Monday"}}, "suggested": 0}}}

func TestAPersonsSessionThatNeedsThemIsInNeedsYouAndSaysSo(t *testing.T) {
	f := newEndTurnFixture(t)
	f.turn(t, f.from, "turn-1", func() {
		if out, refused := callMCP(t, f.person, "agenthail_end_turn", deployAsk); refused || out["needs"] != "you" || out["askedSession"] != nil {
			t.Fatalf("end turn: %v refused=%v", out, refused)
		}
		if code := refusalCode(t, f.person, "agenthail_end_turn", deployAsk); code != "already_ended" {
			t.Fatalf("a second end of one turn: %q", code)
		}
	})
	f.alert(t, " needs you: Deploy the fix?")
	row := viewRow(t, f.d, f.from.ID)
	if row.Attention == nil || row.Attention.Kind != AttentionAsk || row.EndTurn == nil || len(row.EndTurn.Asks) != 1 || !presentationNeedsYou(&row) {
		t.Fatalf("row attention=%+v endTurn=%+v", row.Attention, row.EndTurn)
	}

	f.turn(t, f.from, "turn-2", func() {
		if _, refused := callMCP(t, f.person, "agenthail_end_turn", map[string]any{"headline": "Deployed", "needs": "nothing", "next": []any{"watch the error rate"}}); refused {
			t.Fatal("the next turn could not end")
		}
	})
	f.alert(t, " done: Deployed")
	if row := viewRow(t, f.d, f.from.ID); row.Attention != nil || row.EndTurn == nil || row.EndTurn.Headline != "Deployed" {
		t.Fatalf("after a done turn: attention=%+v endTurn=%+v", row.Attention, row.EndTurn)
	}
}

func TestAWorkersNeedsYouGoesToWhoStartedItNotThePerson(t *testing.T) {
	f := newEndTurnFixture(t)
	f.turn(t, f.to, "turn-1", func() {
		if out, refused := callMCP(t, f.worker, "agenthail_end_turn", deployAsk); refused || out["askedSession"] != f.from.ID {
			t.Fatalf("end turn: %v refused=%v", out, refused)
		}
	})
	f.alert(t, " finished")
	if len(f.desktop.matching(" needs you: Deploy the fix?")) != 0 {
		t.Fatal("the person was told a worker needs them")
	}
	if row := viewRow(t, f.d, f.to.ID); row.Attention != nil {
		t.Fatalf("a worker's ask put it in front of the person: %+v", row.Attention)
	}
	queued, err := f.r.ActiveQueue(f.from.ID, f.d.adapters())
	if err != nil {
		t.Fatal(err)
	}
	told := f.fake.sent
	for _, row := range queued {
		told = append(told, row.Message)
	}
	if len(told) != 1 || !strings.Contains(told[0], "needs you: Deploy the fix?") || !strings.Contains(told[0], "Wait (hold until Monday)") {
		t.Fatalf("the spawner was told %q", told)
	}
}

func viewRow(t *testing.T, d *Daemon, id string) dashboardSession {
	t.Helper()
	d.discoverCatalog(context.Background())
	view, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range view.Sessions {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("%s is not in the session view", id)
	return dashboardSession{}
}

func TestAnEndOfTurnTheAgentCanFixIsRefused(t *testing.T) {
	f := newEndTurnFixture(t)
	ask := func(question string, options ...any) map[string]any {
		return map[string]any{"question": question, "options": options}
	}
	deploy := map[string]any{"label": "Deploy"}
	cases := map[string]map[string]any{
		"a blank headline":           {"headline": "  ", "needs": "nothing"},
		"a two-line headline":        {"headline": "one\ntwo", "needs": "nothing"},
		"a long headline":            {"headline": strings.Repeat("x", 81), "needs": "nothing"},
		"unknown needs":              {"headline": "h", "needs": "maybe"},
		"needs you without an ask":   {"headline": "h", "needs": "you"},
		"asks with needs nothing":    {"headline": "h", "needs": "nothing", "asks": []any{ask("q?", deploy)}},
		"next with needs you":        {"headline": "h", "needs": "you", "asks": []any{ask("q?", deploy)}, "next": []any{"later"}},
		"an ask without options":     {"headline": "h", "needs": "you", "asks": []any{ask("q?")}},
		"suggested out of range":     {"headline": "h", "needs": "you", "asks": []any{map[string]any{"question": "q?", "options": []any{deploy}, "suggested": 1}}},
		"a link that is not a url":   {"headline": "h", "needs": "you", "asks": []any{map[string]any{"question": "q?", "options": []any{deploy}, "links": []any{map[string]any{"title": "PR", "url": "not a url"}}}}},
		"five asks":                  {"headline": "h", "needs": "you", "asks": []any{ask("1", deploy), ask("2", deploy), ask("3", deploy), ask("4", deploy), ask("5", deploy)}},
		"four next items":            {"headline": "h", "needs": "nothing", "next": []any{"a", "b", "c", "d"}},
		"an option label over limit": {"headline": "h", "needs": "you", "asks": []any{ask("q?", map[string]any{"label": strings.Repeat("x", 41)})}},
	}
	for name, args := range cases {
		if code := refusalCode(t, f.person, "agenthail_end_turn", args); code != "invalid" {
			t.Errorf("%s: refusal %q, want invalid", name, code)
		}
	}
	if ends, err := f.r.CurrentTurnEnds(); err != nil || len(ends) != 0 {
		t.Fatalf("a refused end was stored: %+v err=%v", ends, err)
	}
}
