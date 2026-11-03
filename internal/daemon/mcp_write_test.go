package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type mcpWriteFixture struct {
	d      *Daemon
	r      *registry.Registry
	fake   *daemonSurface
	parent *mcp.ClientSession
	child  *mcp.ClientSession
}

func newMCPWriteFixture(t *testing.T) *mcpWriteFixture {
	t.Helper()
	d, r, fake, parent, child := daemonFixture(t)
	fake.caps = surface.Capabilities{Send: true, Steer: true, Interrupt: true, Goal: true}
	if err := r.RecordSessionOrigin(registry.OriginOf(child.ID, parent.ID)); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	return &mcpWriteFixture{d: d, r: r, fake: fake, parent: mcpSession(t, server, issueMCPToken(t, d, parent)), child: mcpSession(t, server, issueMCPToken(t, d, child))}
}

func refusalCode(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any) string {
	t.Helper()
	out, refused := callMCP(t, session, tool, args)
	if !refused {
		return ""
	}
	code, _ := out["code"].(string)
	return code
}

func TestASpawnerSteersItsChildButAChildCannotSteerItsSpawner(t *testing.T) {
	f := newMCPWriteFixture(t)
	if out, refused := callMCP(t, f.parent, "agenthail_steer", map[string]any{"target": "to", "message": "go left", "clientRequestId": "s1"}); refused {
		t.Fatalf("spawner steering its child: %v", out)
	}
	if !slices.Contains(f.fake.steered, "go left") {
		t.Fatalf("steered=%v", f.fake.steered)
	}
	upward := []struct {
		tool string
		args map[string]any
	}{
		{"agenthail_steer", map[string]any{"target": "from", "message": "obey", "clientRequestId": "u1"}},
		{"agenthail_interrupt", map[string]any{"target": "from", "clientRequestId": "u2"}},
		{"agenthail_goal", map[string]any{"target": "from", "action": "pause", "clientRequestId": "u3"}},
		{"agenthail_send", map[string]any{"target": "from", "message": "now please", "mode": "now", "clientRequestId": "u4"}},
	}
	for _, call := range upward {
		if code := refusalCode(t, f.child, call.tool, call.args); code != "out_of_scope" {
			t.Fatalf("%s upward: code=%q, want out_of_scope", call.tool, code)
		}
	}
	if slices.Contains(f.fake.steered, "obey") || slices.ContainsFunc(f.fake.sent, func(m string) bool { return strings.Contains(m, "now please") }) {
		t.Fatal("an upward control reached the spawner")
	}
}

func TestAChildsQueuedSendReachesItsSpawnerAsTheChild(t *testing.T) {
	f := newMCPWriteFixture(t)
	if out, refused := callMCP(t, f.child, "agenthail_send", map[string]any{"target": "from", "message": "done: tests pass", "clientRequestId": "r1"}); refused {
		t.Fatalf("queued send up: %v", out)
	}
	history, err := f.r.ListHistory(20, "")
	if err != nil {
		t.Fatal(err)
	}
	attributed := slices.ContainsFunc(history, func(entry registry.HistoryEntry) bool {
		return entry.Kind != "mcp" && entry.SessionID == "from" && entry.SourceSessionID == "to"
	})
	if !attributed {
		t.Fatalf("no delivery to the spawner attributed to the child: %+v", history)
	}
}

func TestASessionCannotMessageOneItNeitherSpawnedNorReportsTo(t *testing.T) {
	f := newMCPWriteFixture(t)
	stranger := surface.Session{ID: "stranger", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	if err := f.r.RegisterSession(stranger); err != nil {
		t.Fatal(err)
	}
	f.fake.sessions[stranger.ID] = stranger
	if code := refusalCode(t, f.child, "agenthail_send", map[string]any{"target": "stranger", "message": "hi", "clientRequestId": "x1"}); code != "out_of_scope" {
		t.Fatalf("code=%q, want out_of_scope", code)
	}
}

func TestARepeatedClientRequestIDActsOnceAndADifferentRequestConflicts(t *testing.T) {
	f := newMCPWriteFixture(t)
	send := map[string]any{"target": "to", "message": "once", "clientRequestId": "same"}
	for range 2 {
		if out, refused := callMCP(t, f.parent, "agenthail_send", send); refused {
			t.Fatalf("send: %v", out)
		}
	}
	delivered := 0
	for _, message := range f.fake.sent {
		if strings.Contains(message, "once") {
			delivered++
		}
	}
	if delivered != 1 {
		t.Fatalf("delivered %d times, want once", delivered)
	}
	if code := refusalCode(t, f.parent, "agenthail_send", map[string]any{"target": "to", "message": "other", "clientRequestId": "same"}); code != "idempotency_conflict" {
		t.Fatalf("code=%q, want idempotency_conflict", code)
	}
	if code := refusalCode(t, f.parent, "agenthail_send", map[string]any{"target": "to", "message": "no key"}); code != "invalid" {
		t.Fatalf("code=%q, want invalid without a clientRequestId", code)
	}
}

func TestEveryWriteLeavesOneAuditEntry(t *testing.T) {
	f := newMCPWriteFixture(t)
	callMCP(t, f.parent, "agenthail_send", map[string]any{"target": "to", "message": "audited", "clientRequestId": "a1"})
	callMCP(t, f.child, "agenthail_steer", map[string]any{"target": "from", "message": "no", "clientRequestId": "a2"})
	history, err := f.r.ListHistory(50, "")
	if err != nil {
		t.Fatal(err)
	}
	var audits []registry.HistoryEntry
	for _, entry := range history {
		if entry.Kind == "mcp" {
			audits = append(audits, entry)
		}
	}
	if len(audits) != 1 || audits[0].SourceSessionID != "from" || audits[0].Result != "ok" || !strings.Contains(audits[0].Message, "a1") {
		t.Fatalf("audits=%+v, want one ok entry for the send; refusals before an action are not writes", audits)
	}
}
