package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func personChangesRole(t *testing.T, d *Daemon, request daemonapi.AgentRoleRequest) {
	t.Helper()
	if err := d.changeAgentRole(context.Background(), daemonapi.CLIActor, request); err != nil {
		t.Fatal(err)
	}
}

func workAdd(project, text string) map[string]any {
	request := map[string]any{"op": "work", "clientRequestId": "add-" + text, "request": map[string]any{"action": "add", "text": text, "fields": map[string]any{"type": "task", "priority": "P2"}}}
	if project != "" {
		request["project"] = project
	}
	return request
}

func workCount(t *testing.T, d *Daemon, key string) int {
	t.Helper()
	board, err := d.ledgerStore().Board(key)
	if err != nil {
		t.Fatal(err)
	}
	return len(board.Ready())
}

func TestARemoteIdentityReadsAndWritesTheLedgerOfAProjectItNames(t *testing.T) {
	f := newRemoteFixture(t, "")
	alpha := addLedgerProject(t, f.r, "alpha", "repo:github.com/acme/alpha")
	beta := addLedgerProject(t, f.r, "beta", "repo:github.com/acme/beta")
	session := f.connect(t, map[string]string{"Authorization": "Bearer " + f.token}, nil)

	refusal, refused := callMCP(t, session, "agenthail_ledger_read", nil)
	if message, _ := refusal["message"].(string); !refused || refusal["code"] != "invalid" || !strings.Contains(message, "alpha") || !strings.Contains(message, "beta") {
		t.Fatalf("a read naming no project = %v, want a refusal that names the projects", refusal)
	}
	if board, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"project": "alpha"}); refused || board["projectKey"] != alpha.key {
		t.Fatalf("read alpha by name = %v", board)
	}
	if board, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"project": beta.key, "view": "backlog"}); refused {
		t.Fatalf("read beta's backlog by id = %v", board)
	}
	if out, refused := callMCP(t, session, "agenthail_ledger_write", workAdd("beta", "index the docs")); refused {
		t.Fatalf("write beta = %v", out)
	}
	if workCount(t, f.d, beta.key) != 1 || workCount(t, f.d, alpha.key) != 0 {
		t.Fatal("the work item did not land in beta alone")
	}
	history, err := f.r.ListHistory(50, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(history, func(entry registry.HistoryEntry) bool {
		return entry.Kind == "mcp-remote" && entry.Message == "agenthail_ledger_write work add in "+beta.key+" via token only" && entry.Result == "ok"
	}) {
		t.Fatalf("no remote history entry naming the write in %+v", history)
	}
	if out, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"project": "gamma"}); !refused || out["code"] != "not_found" {
		t.Fatalf("an unknown project = %v", out)
	}

	personChangesRole(t, f.d, daemonapi.AgentRoleRequest{Op: "put", Name: "alpha-only", Role: agentroles.Role{Reach: agentroles.ReachAll, Ledgers: []string{alpha.key}}})
	personChangesRole(t, f.d, daemonapi.AgentRoleRequest{Op: "assign", Name: "alpha-only", Kind: registry.AssignIdentity, Target: "scout"})
	if out, refused := callMCP(t, session, "agenthail_ledger_write", workAdd("beta", "second")); !refused || out["code"] != "capability_missing" {
		t.Fatalf("a write outside the role's ledgers = %v", out)
	}
	refusal, refused = callMCP(t, session, "agenthail_ledger_read", nil)
	if message, _ := refusal["message"].(string); !refused || !strings.Contains(message, "alpha") || strings.Contains(message, "beta") {
		t.Fatalf("a narrowed role's refusal = %v, want only the projects it reaches", refusal)
	}
	if out, refused := callMCP(t, session, "agenthail_ledger_write", workAdd("alpha", "first")); refused || workCount(t, f.d, alpha.key) != 1 {
		t.Fatalf("a write inside the role's ledgers = %v", out)
	}
}

func TestASessionNamesAnotherProjectsLedgerOnlyWhenItsRoleReachesIt(t *testing.T) {
	d, r, _, _, _, widget := questionDaemon(t)
	other := addLedgerProject(t, r, "other", "repo:github.com/acme/other")
	server := mcpServer(t, d)
	worker, _ := r.Session("s1")
	session := mcpSession(t, server, issueMCPToken(t, d, *worker))

	if board, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"project": widget.key}); refused || board["projectKey"] != widget.key {
		t.Fatalf("naming its own project = %v", board)
	}
	if out, refused := callMCP(t, session, "agenthail_ledger_write", workAdd("other", "elsewhere")); !refused || out["code"] != "capability_missing" {
		t.Fatalf("a worker writing another project's ledger = %v", out)
	}
	scope := func() *mcpRefusal {
		t.Helper()
		caller, err := d.agentCaller(registry.CallerRecord{Session: *worker})
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.agentScope(httptest.NewRequest(http.MethodPost, "/api/v1/scope", nil), caller, daemonapi.ScopeRequest{Action: "ledger", Project: other.key})
		refusal, _ := err.(*mcpRefusal)
		if err != nil && refusal == nil {
			t.Fatal(err)
		}
		return refusal
	}
	if refusal := scope(); refusal == nil || refusal.Detail["rule"] != "different_project" {
		t.Fatalf("the CLI's ledger scope for another project = %+v", refusal)
	}

	changeRole(t, r, agentroles.Worker, func(role *agentroles.Role) { role.Ledgers = []string{other.key} })
	if out, refused := callMCP(t, session, "agenthail_ledger_write", workAdd("other", "elsewhere")); refused || workCount(t, d, other.key) != 1 {
		t.Fatalf("a granted worker writing another project's ledger = %v", out)
	}
	if out, refused := callMCP(t, session, "agenthail_ledger_write", workAdd("", "at home")); refused || workCount(t, d, widget.key) != 1 {
		t.Fatalf("a write naming no project = %v, want it in the session's own", out)
	}
	if refusal := scope(); refusal != nil {
		t.Fatalf("the CLI's ledger scope after the grant = %+v", refusal)
	}
}

func TestAProjectKnownOnlyFromAPairedHostNamesItsLedger(t *testing.T) {
	f := newRemoteFixture(t, "")
	const far = "h0123456789ab"
	if _, err := f.r.AddHost(registry.PairedHost{ID: far, Name: "far", Endpoint: "https://far.example", Address: "100.64.0.2", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	mirror := func(session, project string) {
		t.Helper()
		row := registry.HostCatalogRow{Session: surface.Session{ID: session, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}, EntityID: far + "::" + session, Projection: json.RawMessage(`{}`), Project: json.RawMessage(project), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now()}
		if _, _, err := f.r.ApplyHostCatalogRow(far, row); err != nil {
			t.Fatal(err)
		}
	}
	mirror("n1", `{"id":"proj-notes","localId":"proj-notes","displayName":"notes","kind":"folder","added":true}`)
	mirror("w1", `{"id":"repo:github.com/acme/widget","localId":"proj-w","displayName":"widget","kind":"git","added":true,"key":"repo:github.com/acme/widget"}`)
	mirror("i1", `{"id":"implicit:/srv/scratch","localId":"implicit:/srv/scratch","displayName":"scratch","kind":"git"}`)
	for name, want := range map[string]string{"notes": registry.LedgerProjectKey("proj-notes"), "widget": "repo:github.com/acme/widget"} {
		named, err := broadcast.FindProject(f.r, name)
		if err != nil {
			t.Fatal(err)
		}
		if key, err := f.d.projectLedgerKey(named); err != nil || key != want {
			t.Fatalf("%s's ledger = %q, %v; want %q", name, key, err, want)
		}
	}
	session := f.connect(t, map[string]string{"Authorization": "Bearer " + f.token}, nil)
	if out, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"project": "scratch"}); !refused || out["code"] != "not_found" {
		t.Fatalf("a folder with no ledger = %v", out)
	}
	personChangesRole(t, f.d, daemonapi.AgentRoleRequest{Op: "put", Name: "notes-only", Role: agentroles.Role{Reach: agentroles.ReachAll, Ledgers: []string{"proj-notes"}}})
	personChangesRole(t, f.d, daemonapi.AgentRoleRequest{Op: "assign", Name: "notes-only", Kind: registry.AssignIdentity, Target: "scout"})
	caller, err := f.d.remoteCaller(&auth.TokenInfo{UserID: f.scout.SessionID, Extra: map[string]any{tokenInfoRemote: "scout", tokenInfoBinding: "token only"}})
	if err != nil {
		t.Fatal(err)
	}
	if key, err := f.d.callerLedgerKey(caller, "notes"); err != nil || key != registry.LedgerProjectKey("proj-notes") {
		t.Fatalf("a granted paired-host project = %q, %v", key, err)
	}
	if _, err := f.d.callerLedgerKey(caller, "widget"); err == nil {
		t.Fatal("a project outside the role's ledgers was let through")
	}
}
