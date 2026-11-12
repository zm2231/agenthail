package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

type requestingSurface struct {
	*daemonSurface
	open []surface.Request
}

func (s *requestingSurface) Requests(context.Context, *surface.Session) (surface.RequestState, error) {
	return surface.RequestState{PermissionMode: "default", Requests: s.open}, nil
}

func (s *requestingSurface) Respond(context.Context, *surface.Session, string, surface.Decision) (string, error) {
	return "", nil
}

func initializeMCP(t *testing.T, server string, token, version string) string {
	t.Helper()
	body := strings.Replace(mcpInitialize, "2025-06-18", version, 1)
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, server+"/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var reply struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&reply); err != nil {
		t.Fatalf("version %s: status %d: %v", version, response.StatusCode, err)
	}
	return reply.Result.ProtocolVersion
}

func TestMCPNegotiatesOnlyThePinnedRevisions(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	server := mcpServer(t, d)
	token := issueMCPToken(t, d, from)
	for requested, want := range map[string]string{"2025-06-18": "2025-06-18", "2025-11-25": "2025-11-25", "2025-03-26": "2025-11-25", "2024-11-05": "2025-11-25"} {
		if got := initializeMCP(t, server.URL, token, requested); got != want {
			t.Fatalf("requested %s negotiated %q, want %s", requested, got, want)
		}
	}
}

func traverseSessions(t *testing.T, session *mcp.ClientSession, between func(page int)) map[string]int {
	t.Helper()
	seen := map[string]int{}
	cursor := ""
	for page := 0; page < 20; page++ {
		args := map[string]any{"filter": "project", "limit": 2}
		if cursor != "" {
			args["cursor"] = cursor
		}
		listing, refused := callMCP(t, session, "agenthail_sessions", args)
		if refused {
			t.Fatalf("page %d: %v", page, listing)
		}
		for _, id := range sessionIDs(t, listing) {
			seen[id]++
		}
		between(page)
		next, _ := listing["nextCursor"].(string)
		if next == "" {
			return seen
		}
		cursor = next
	}
	t.Fatal("paging did not end")
	return nil
}

func TestMCPSessionTraversalReturnsEveryStandingSessionOnce(t *testing.T) {
	d, r, _, _, _ := daemonFixture(t)
	project := addLedgerProject(t, r, "p-pages", "pages")
	lead := project.session(t, r, "page-lead")
	for index := range 5 {
		project.session(t, r, fmt.Sprintf("page-%d", index))
	}
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, lead))
	seen := traverseSessions(t, session, func(page int) {
		if page != 0 {
			return
		}
		moved, _ := r.Session("page-4")
		moved.LastActive = time.Now().Add(time.Hour)
		if err := r.RegisterSession(*moved); err != nil {
			t.Fatal(err)
		}
		if _, err := r.WriteDB().Exec(`DELETE FROM sessions WHERE id IN ('page-3','page-4')`); err != nil {
			t.Fatal(err)
		}
		project.session(t, r, "page-new")
	})
	for _, id := range []string{"page-lead", "page-0", "page-1", "page-2"} {
		if seen[id] != 1 {
			t.Fatalf("%s seen %d times: %v", id, seen[id], seen)
		}
	}
	for id, count := range seen {
		if count > 1 {
			t.Fatalf("%s repeated: %v", id, seen)
		}
	}
	if again := traverseSessions(t, session, func(int) {}); again["page-new"] != 1 {
		t.Fatalf("the next traversal misses the session that joined: %v", again)
	}
}

func TestMCPRequestsReturnEveryOpenRequestInOneCall(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	base := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	responder := &requestingSurface{daemonSurface: base}
	d := newTestDaemon(t, r, []surface.Surface{responder})
	worker := surface.Session{ID: "asker", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle}
	if err := r.RegisterSession(worker); err != nil {
		t.Fatal(err)
	}
	base.sessions[worker.ID] = worker
	created := time.Now().Add(-time.Hour)
	for index := range 9 {
		responder.open = append(responder.open, surface.Request{ID: fmt.Sprintf("req-%d", index), Kind: surface.WaitingApproval, Tool: "Bash", Title: strings.Repeat("x", 3800), CreatedAt: created.Add(time.Duration(index%3) * time.Second)})
	}
	responder.open = append(responder.open, surface.Request{ID: "req-same-ms", Kind: surface.WaitingApproval, Tool: "Bash", CreatedAt: created})
	refs := make([]string, 0, len(responder.open))
	for _, request := range responder.open {
		refs = append(refs, request.ID)
	}
	d.syncWaits(worker, &surface.Waiting{Kind: surface.WaitingApproval, ID: refs[0], Answerable: true, Open: refs})
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, worker))
	result, refused := callMCP(t, session, "agenthail_requests", map[string]any{"sessionId": worker.ID})
	requests, _ := result["requests"].([]any)
	if refused || len(requests) != 10 || result["total"] != 10.0 || result["truncated"] != false {
		t.Fatalf("requests=%d total=%v truncated=%v refused=%v", len(requests), result["total"], result["truncated"], refused)
	}
	seen := map[string]bool{}
	for _, request := range requests {
		row := request.(map[string]any)
		seen[row["id"].(string)] = true
		if row["id"] != askIDOfRequest(t, r, worker.ID, "req-same-ms") && row["clipped"] != true {
			t.Fatalf("a long prompt was not clipped to fit: %v", row["id"])
		}
	}
	if len(seen) != 10 {
		t.Fatalf("ids=%v", seen)
	}
	one, refused := callMCP(t, session, "agenthail_requests", map[string]any{"sessionId": worker.ID, "requestId": askIDOfRequest(t, r, worker.ID, "req-4")})
	full, _ := one["request"].(map[string]any)
	if refused || len(full["prompt"].(string)) != 3800 || full["clipped"] == true {
		t.Fatalf("one request=%v", one)
	}
	if refusal, refused := callMCP(t, session, "agenthail_requests", map[string]any{"sessionId": worker.ID, "requestId": "answered"}); !refused || refusal["code"] != "not_found" {
		t.Fatalf("missing request=%v", refusal)
	}
}

func TestMCPLedgerPagesFollowCreationOrder(t *testing.T) {
	d, _, handler, _, _, widget := questionDaemon(t)
	for index := range 12 {
		ledgerWrite(t, handler, "/api/v1/ledger/goal", ledgerGoalRequest{ProjectKey: widget.key, SessionID: "s1", Name: fmt.Sprintf("Goal %d", index)}, nil)
	}
	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, *worker))
	var ids []string
	cursor := ""
	for page := 0; page < 10; page++ {
		args := map[string]any{"view": "goals", "limit": 5}
		if cursor != "" {
			args["cursor"] = cursor
		}
		result, refused := callMCP(t, session, "agenthail_ledger_read", args)
		if refused {
			t.Fatalf("page %d: %v", page, result)
		}
		goals, _ := result["goals"].([]any)
		for _, goal := range goals {
			ids = append(ids, goal.(map[string]any)["id"].(string))
		}
		next, _ := result["nextCursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(ids) != 12 || ids[0] != "widget-G1" || ids[1] != "widget-G2" || ids[9] != "widget-G10" || ids[11] != "widget-G12" {
		t.Fatalf("goal order %v", ids)
	}
	ready, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "ready"})
	if refused || ready["total"] == nil {
		t.Fatalf("ready=%v", ready)
	}
}
