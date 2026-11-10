package daemon

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

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

func TestASessionCannotMessageOneOutsideItsProject(t *testing.T) {
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

func TestAQueuedSendReachesAPeerInTheSameProjectButCannotControlIt(t *testing.T) {
	d, r, fake, worker, peer := daemonFixture(t)
	fake.caps = surface.Capabilities{Send: true, Steer: true, Interrupt: true, Goal: true}
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	for _, session := range []surface.Session{worker, peer} {
		session.Cwd, session.LastActive = widget, time.Now()
		fake.sessions[session.ID] = session
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	caller := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, worker))
	if out, refused := callMCP(t, caller, "agenthail_send", map[string]any{"target": peer.ID, "message": "peer: rebased on main", "clientRequestId": "p1"}); refused {
		t.Fatalf("queued send to a peer: %v", out)
	}
	if !slices.ContainsFunc(fake.sent, func(m string) bool { return strings.Contains(m, "peer: rebased on main") }) {
		t.Fatalf("sent=%q, want the peer's message delivered", fake.sent)
	}
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"agenthail_send", map[string]any{"target": peer.ID, "message": "peer now", "mode": "now", "clientRequestId": "p2"}},
		{"agenthail_steer", map[string]any{"target": peer.ID, "message": "peer steer", "clientRequestId": "p3"}},
		{"agenthail_interrupt", map[string]any{"target": peer.ID, "clientRequestId": "p4"}},
	} {
		if code := refusalCode(t, caller, call.tool, call.args); code != "out_of_scope" {
			t.Fatalf("%s to a peer: code=%q, want out_of_scope", call.tool, code)
		}
	}
	if slices.Contains(fake.steered, "peer steer") || slices.ContainsFunc(fake.sent, func(m string) bool { return strings.Contains(m, "peer now") }) {
		t.Fatal("a control write reached a peer")
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

func mcpAudits(t *testing.T, r *registry.Registry) []registry.HistoryEntry {
	t.Helper()
	history, err := r.ListHistory(50, "")
	if err != nil {
		t.Fatal(err)
	}
	var audits []registry.HistoryEntry
	for _, entry := range history {
		if entry.Kind == "mcp" {
			audits = append(audits, entry)
		}
	}
	return audits
}

func TestEveryWriteAndEveryScopeRefusalLeavesOneAuditEntry(t *testing.T) {
	f := newMCPWriteFixture(t)
	callMCP(t, f.parent, "agenthail_send", map[string]any{"target": "to", "message": "audited", "clientRequestId": "a1"})
	upward := map[string]map[string]any{
		"agenthail_steer":     {"target": "from", "message": "no", "clientRequestId": "a2"},
		"agenthail_interrupt": {"target": "from", "clientRequestId": "a3"},
		"agenthail_goal":      {"target": "from", "action": "pause", "clientRequestId": "a4"},
		"agenthail_send":      {"target": "from", "message": "now", "mode": "now", "clientRequestId": "a5"},
	}
	for tool, args := range upward {
		if code := refusalCode(t, f.child, tool, args); code != "out_of_scope" {
			t.Fatalf("%s: code=%q, want out_of_scope", tool, code)
		}
	}
	audits := mcpAudits(t, f.r)
	if len(audits) != 1+len(upward) {
		t.Fatalf("audits=%+v, want the send and one entry per refusal", audits)
	}
	refused := map[string]bool{}
	for _, entry := range audits {
		switch entry.Result {
		case "ok":
			if entry.SourceSessionID != "from" || !strings.Contains(entry.Message, "a1") {
				t.Fatalf("send audit=%+v", entry)
			}
		case "out_of_scope":
			tool, rule, _ := strings.Cut(entry.Message, " ")
			if entry.SourceSessionID != "to" || entry.SessionID != "from" || rule != "rule=down_only" || entry.Error == "" {
				t.Fatalf("refusal audit=%+v, want caller, target, rule and the refusal", entry)
			}
			refused[tool] = true
		default:
			t.Fatalf("unexpected audit %+v", entry)
		}
	}
	for tool := range upward {
		if !refused[tool] {
			t.Fatalf("%s's refusal left no audit entry: %+v", tool, audits)
		}
	}
}

func TestAnActionAuditNamesItsSessionChannelOrProject(t *testing.T) {
	for want, request := range map[string]dashboardActionRequest{
		"s1":         {SessionID: "s1", Channel: "ignored"},
		"#docs":      {Channel: "docs"},
		"project:p1": {Project: "p1"},
	} {
		if got := mcpAuditTarget(request); got != want {
			t.Fatalf("target=%q, want %q", got, want)
		}
	}
}

func TestAScopeRefusalIsAuditedOnceAndOtherRefusalsAreNot(t *testing.T) {
	f := newMCPWriteFixture(t)
	caller := &mcpCaller{session: surface.Session{ID: "to"}}
	recorded := scopeRefusal("from", "down_only", "no")
	recorded.audited = true
	f.d.recordMCPScopeRefusal(caller, "agenthail_steer", recorded)
	f.d.recordMCPScopeRefusal(caller, "agenthail_read", refuse("not_found", "No session x."))
	f.d.recordMCPScopeRefusal(caller, "agenthail_session_settings", (&mcpRefusal{Code: "exceeds_ceiling", Message: "above", Detail: map[string]any{"axis": "permission"}}).about("from"))
	audits := mcpAudits(t, f.r)
	if len(audits) != 1 || audits[0].Message != "agenthail_session_settings rule=ceiling_permission" || audits[0].SessionID != "from" || audits[0].Result != "exceeds_ceiling" {
		t.Fatalf("audits=%+v, want only the ceiling refusal", audits)
	}
}
