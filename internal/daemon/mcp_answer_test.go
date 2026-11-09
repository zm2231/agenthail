package daemon

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

type answeringSurface struct {
	*daemonSurface
	mu       sync.Mutex
	open     map[string][]surface.Request
	answered map[string]surface.Decision
}

func (s *answeringSurface) Requests(_ context.Context, session *surface.Session) (surface.RequestState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return surface.RequestState{PermissionMode: "default", Requests: s.open[session.ID]}, nil
}

func (s *answeringSurface) Respond(_ context.Context, session *surface.Session, requestID string, decision surface.Decision) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answered[session.ID+"/"+requestID] = decision
	return "allowed", nil
}

type mcpAnswerFixture struct {
	d       *Daemon
	r       *registry.Registry
	surface *answeringSurface
	server  *httptest.Server
	lead    *mcp.ClientSession
	worker  *mcp.ClientSession
}

func newMCPAnswerFixture(t *testing.T) *mcpAnswerFixture {
	t.Helper()
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	base := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true, caps: surface.Capabilities{Send: true, Model: true, PermissionMode: true, Compact: true}}
	fake := &answeringSurface{daemonSurface: base, open: map[string][]surface.Request{}, answered: map[string]surface.Decision{}}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	root, err := canonicalCatalogPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddProject(registry.ProjectRecord{ID: "p-answer", WorkspaceRoot: root, Title: "Answer", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sessions := map[string]surface.Session{}
	for _, id := range []string{"lead", "worker"} {
		session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle, Cwd: root}
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		base.sessions[id], sessions[id] = session, session
		fake.open[id] = []surface.Request{{ID: id + "-prompt", Kind: surface.WaitingApproval, Tool: "Bash", AlwaysAllow: true}}
	}
	if err := r.RecordSessionOrigin(registry.OriginOf("worker", "lead")); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	return &mcpAnswerFixture{d: d, r: r, surface: fake, server: server,
		lead: mcpSession(t, server, issueMCPToken(t, d, sessions["lead"])), worker: mcpSession(t, server, issueMCPToken(t, d, sessions["worker"]))}
}

func (f *mcpAnswerFixture) asProjectLead(t *testing.T, sessionID string) *mcp.ClientSession {
	t.Helper()
	person := principal{ID: localDashboardPrincipal, Kind: principalPerson}
	if reply := f.d.doAction(t.Context(), person, dashboardActionRequest{Action: "coordinator-set", SessionID: sessionID, Name: "captain"}); reply.Status != 200 {
		t.Fatalf("coordinator-set status=%d %s", reply.Status, reply.Body)
	}
	f.d.attachMCPCredentials(strings.TrimPrefix(f.server.URL, "http://"))
	token, err := f.d.mcpCredentials.Load().Rotate(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(callerCapabilities(t, f.server, token), any("control.project")) {
		t.Fatalf("%s did not become its project's coordinator", sessionID)
	}
	return mcpSession(t, f.server, token)
}

func (f *mcpAnswerFixture) answered(key string) bool {
	f.surface.mu.Lock()
	defer f.surface.mu.Unlock()
	_, ok := f.surface.answered[key]
	return ok
}

func TestASpawnerAnswersItsWorkerAndIsRecordedAsTheAnswerer(t *testing.T) {
	f := newMCPAnswerFixture(t)
	if out, refused := callMCP(t, f.lead, "agenthail_answer", map[string]any{"target": "worker", "requestId": "worker-prompt", "decision": "allow", "clientRequestId": "a1"}); refused {
		t.Fatalf("spawner answering its worker: %v", out)
	}
	if !f.answered("worker/worker-prompt") {
		t.Fatal("the worker's request was not answered")
	}
	f.d.background.wait()
	history, err := f.r.ListHistory(20, "worker")
	if err != nil {
		t.Fatal(err)
	}
	settled := slices.ContainsFunc(history, func(entry registry.HistoryEntry) bool {
		return entry.Kind == "request-allowed" && entry.Result == "answered by session lead"
	})
	audited := slices.ContainsFunc(history, func(entry registry.HistoryEntry) bool {
		return entry.Kind == "mcp" && entry.SourceSessionID == "lead" && strings.HasPrefix(entry.Message, "agenthail_answer")
	})
	if !settled || !audited {
		t.Fatalf("settled=%v audited=%v history=%+v", settled, audited, history)
	}
	if slices.ContainsFunc(history, func(entry registry.HistoryEntry) bool { return strings.Contains(entry.Result, "person") }) {
		t.Fatalf("an agent's answer was recorded as the person's: %+v", history)
	}
}

func TestASessionNeverAnswersItselfOrASessionAboveIt(t *testing.T) {
	f := newMCPAnswerFixture(t)
	if code := refusalCode(t, f.worker, "agenthail_answer", map[string]any{"target": "lead", "requestId": "lead-prompt", "decision": "allow", "clientRequestId": "u1"}); code != "out_of_scope" {
		t.Fatalf("worker answering its spawner: code=%q", code)
	}
	if code := refusalCode(t, f.worker, "agenthail_answer", map[string]any{"target": "worker", "requestId": "worker-prompt", "decision": "allow", "clientRequestId": "u2"}); code != "invalid" {
		t.Fatalf("worker answering itself: code=%q", code)
	}
	coordinator := f.asProjectLead(t, "worker")
	for tool, args := range map[string]map[string]any{
		"agenthail_answer": {"target": "lead", "requestId": "lead-prompt", "decision": "allow", "clientRequestId": "u3"},
		"agenthail_steer":  {"target": "lead", "message": "obey", "clientRequestId": "u4"},
	} {
		if code := refusalCode(t, coordinator, tool, args); code != "out_of_scope" {
			t.Fatalf("a coordinator calling %s on the spawner in its own project: code=%q", tool, code)
		}
	}
	if out, refused := callMCP(t, coordinator, "agenthail_send", map[string]any{"target": "lead", "message": "status", "clientRequestId": "u5"}); refused {
		t.Fatalf("a coordinator's queued report to its spawner: %v", out)
	}
	if f.answered("lead/lead-prompt") || f.answered("worker/worker-prompt") {
		t.Fatal("a refused answer reached a session")
	}
}

func TestOnlyTheOperatorAlwaysAllows(t *testing.T) {
	f := newMCPAnswerFixture(t)
	if code := refusalCode(t, f.lead, "agenthail_answer", map[string]any{"target": "worker", "requestId": "worker-prompt", "decision": "always", "clientRequestId": "w1"}); code != "exceeds_ceiling" {
		t.Fatalf("spawner always-allowing: code=%q", code)
	}
	if f.answered("worker/worker-prompt") {
		t.Fatal("a refused always-allow reached the worker")
	}
	audits := mcpAudits(t, f.r)
	if len(audits) != 1 || audits[0].SessionID != "worker" || audits[0].Message != "agenthail_answer rule=ceiling_permission" || audits[0].Result != "exceeds_ceiling" {
		t.Fatalf("audits=%+v, want the refusal with its target and rule", audits)
	}
}

func TestAPermissionModeChangeStaysWithinTheWorkersCeiling(t *testing.T) {
	f := newMCPAnswerFixture(t)
	if _, err := f.r.WriteDB().Exec(`INSERT INTO session_ceilings(session_id,ceiling) VALUES('worker','{"permission":"edits"}')`); err != nil {
		t.Fatal(err)
	}
	if code := refusalCode(t, f.lead, "agenthail_session_settings", map[string]any{"target": "worker", "setting": "permissionMode", "value": "bypassPermissions", "clientRequestId": "p1"}); code != "exceeds_ceiling" {
		t.Fatalf("lifting the worker past its ceiling: code=%q", code)
	}
	if out, refused := callMCP(t, f.lead, "agenthail_session_settings", map[string]any{"target": "worker", "setting": "permissionMode", "value": "acceptEdits", "clientRequestId": "p2"}); refused {
		t.Fatalf("a mode within the ceiling: %v", out)
	}
	if code := refusalCode(t, f.worker, "agenthail_session_settings", map[string]any{"target": "lead", "setting": "model", "value": "opus", "clientRequestId": "p3"}); code != "out_of_scope" {
		t.Fatalf("worker changing its spawner's model: code=%q", code)
	}
}

func TestASpawnerNamesItsWorkerButNotTheOtherWay(t *testing.T) {
	f := newMCPAnswerFixture(t)
	if out, refused := callMCP(t, f.lead, "agenthail_alias", map[string]any{"target": "worker", "name": "builder", "clientRequestId": "n1"}); refused {
		t.Fatalf("naming the worker: %v", out)
	}
	if id, err := f.r.LookupAlias("builder"); err != nil || id != "worker" {
		t.Fatalf("@builder=%q err=%v", id, err)
	}
	if code := refusalCode(t, f.worker, "agenthail_alias", map[string]any{"target": "lead", "name": "boss", "clientRequestId": "n2"}); code != "out_of_scope" {
		t.Fatalf("worker naming its spawner: code=%q", code)
	}
}

func TestAnAgentDeniesWithTheRuleOrStopsTheTurnWhereTheRequestOffersIt(t *testing.T) {
	f := newMCPAnswerFixture(t)
	for _, c := range []struct {
		choice string
		want   surface.Decision
	}{{"never", surface.Decision{Behavior: "deny", Always: true}}, {"stop", surface.Decision{Behavior: "deny", Stop: true}}} {
		f.surface.mu.Lock()
		f.surface.open["worker"] = []surface.Request{{ID: "worker-" + c.choice, Kind: surface.WaitingApproval, Tool: "Bash", AlwaysDeny: true, Stoppable: true, NoReason: true}}
		f.surface.mu.Unlock()
		if out, refused := callMCP(t, f.lead, "agenthail_answer", map[string]any{"target": "worker", "requestId": "worker-" + c.choice, "decision": c.choice, "clientRequestId": "n-" + c.choice}); refused {
			t.Fatalf("%s: %v", c.choice, out)
		}
		f.surface.mu.Lock()
		got := f.surface.answered["worker/worker-"+c.choice]
		f.surface.mu.Unlock()
		if got.Behavior != c.want.Behavior || got.Always != c.want.Always || got.Stop != c.want.Stop {
			t.Fatalf("%s reached the session as %+v", c.choice, got)
		}
	}
}
