package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

func spawnBearer(t *testing.T, spawn claudehosttest.Spawn) string {
	t.Helper()
	for index, arg := range spawn.Argv {
		value, inline := strings.CutPrefix(arg, "--mcp-config=")
		if !inline {
			if arg != "--mcp-config" || index+1 >= len(spawn.Argv) {
				continue
			}
			value = spawn.Argv[index+1]
		}
		var config struct {
			MCPServers map[string]struct {
				Headers map[string]string `json:"headers"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(value), &config); err != nil {
			t.Fatal(err)
		}
		return strings.TrimPrefix(config.MCPServers["agenthail"].Headers["Authorization"], "Bearer ")
	}
	return ""
}

type mcpClaudeFixture struct {
	host     *claudehosttest.Fixture
	registry *registry.Registry
	daemon   *Daemon
	server   *httptest.Server
}

func newMCPClaudeFixture(t *testing.T) *mcpClaudeFixture {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	host.Start(t)
	r := hostRegistry(t)
	d := newTestDaemon(t, r, []surface.Surface{surfaces.NewClaude(nil, host.Home)})
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: dashboardTestToken}))
	t.Cleanup(server.Close)
	d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	return &mcpClaudeFixture{host: host, registry: r, daemon: d, server: server}
}

func (f *mcpClaudeFixture) action(t *testing.T, body string) map[string]any {
	t.Helper()
	w := serveDashboardRequest(dashboardRouter(f.daemon), http.MethodPost, "/api/action", body)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code >= 300 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	return out
}

func (f *mcpClaudeFixture) create(t *testing.T) (string, string) {
	t.Helper()
	created := f.action(t, fmt.Sprintf(`{"action":"session-create","surface":"claude","message":"hello","cwd":%q}`, f.host.Cwd))
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	spawns := f.host.Spawns(t, id)
	if id == "" || len(spawns) != 1 {
		t.Fatalf("id=%q spawns=%d", id, len(spawns))
	}
	return id, spawnBearer(t, spawns[0])
}

func (f *mcpClaudeFixture) replacedTokens(t *testing.T, sessionID string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.host.ClaudeDir, "controls.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var tokens []string
	for _, line := range claudehosttest.Lines(data) {
		var control struct {
			SessionID string `json:"sessionId"`
			Subtype   string `json:"subtype"`
			Servers   map[string]struct {
				Headers map[string]string `json:"headers"`
			} `json:"servers"`
		}
		if err := json.Unmarshal(line, &control); err != nil {
			t.Fatal(err)
		}
		if control.SessionID == sessionID && control.Subtype == "mcp_set_servers" {
			tokens = append(tokens, strings.TrimPrefix(control.Servers["agenthail"].Headers["Authorization"], "Bearer "))
		}
	}
	return tokens
}

func (f *mcpClaudeFixture) capabilities(t *testing.T, token string) []any {
	t.Helper()
	whoami, refused := callMCP(t, mcpSession(t, f.server, token), "agenthail_whoami", nil)
	if refused {
		t.Fatalf("whoami refused: %v", whoami)
	}
	capabilities, _ := whoami["capabilities"].([]any)
	return capabilities
}

func TestACreatedClaudeHostSessionReachesTheMCPServerAsItself(t *testing.T) {
	f := newMCPClaudeFixture(t)
	id, token := f.create(t)
	if whoami, refused := callMCP(t, mcpSession(t, f.server, token), "agenthail_whoami", nil); refused || whoami["sessionId"] != id {
		t.Fatalf("whoami=%v refused=%v", whoami, refused)
	}
}

func (f *mcpClaudeFixture) addProject(t *testing.T) {
	t.Helper()
	root, err := canonicalCatalogPath(f.host.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.registry.AddProject(registry.ProjectRecord{ID: "p-lead", WorkspaceRoot: root, Title: "Lead", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func TestACoordinatorStartedByItsFirstMessageHoldsItsLeadCredentialAtOnce(t *testing.T) {
	f := newMCPClaudeFixture(t)
	f.addProject(t)
	f.action(t, fmt.Sprintf(`{"action":"coordinator-new","surface":"claude","cwd":%q,"name":"captain"}`, f.host.Cwd))
	sent := f.action(t, `{"action":"coordinator-send","name":"captain","message":"hello"}`)
	if sent["started"] != true {
		t.Fatalf("send did not start the coordinator: %s", redacted(sent))
	}
	spec, err := f.registry.CoordinatorNamed("captain")
	if err != nil || spec.SessionID == "" {
		t.Fatalf("spec=%+v err=%v", spec, err)
	}
	spawns := f.host.Spawns(t, spec.SessionID)
	if len(spawns) != 1 || !slices.Contains(f.capabilities(t, spawnBearer(t, spawns[0])), any("control.project")) {
		t.Fatalf("spawns=%d, the coordinator's first credential cannot control its project", len(spawns))
	}
	if replaced := f.replacedTokens(t, spec.SessionID); len(replaced) != 0 {
		t.Fatalf("the coordinator's credential was swapped after its first turn began: %d swaps", len(replaced))
	}
}

func TestAPersonsMessageThatStartsACoordinatorIsThePersonsTurn(t *testing.T) {
	f := newMCPClaudeFixture(t)
	f.addProject(t)
	f.action(t, fmt.Sprintf(`{"action":"coordinator-new","surface":"claude","cwd":%q,"name":"captain"}`, f.host.Cwd))
	if sent := f.action(t, `{"action":"coordinator-send","name":"captain","message":"hello","author":"person"}`); sent["started"] != true {
		t.Fatalf("send did not start the coordinator: %s", redacted(sent))
	}
	spec, err := f.registry.CoordinatorNamed("captain")
	if err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the first turn", 15*time.Second, func() bool { return len(f.host.Turns(t, spec.SessionID)) > 0 })
	turns := f.host.Turns(t, spec.SessionID)
	if len(turns) != 1 || turnOrigin(t, turns[0]) != "human" {
		t.Fatalf("turns = %+v, want one human turn", turns)
	}
}

func TestAPersonsMessageThatStartsACoordinatorIsCapturedOnceAsTheirWords(t *testing.T) {
	f := newMCPClaudeFixture(t)
	root, err := canonicalCatalogPath(f.host.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	const key = "repo:example.com/lead"
	if err := f.registry.AddProject(registry.ProjectRecord{ID: "p-lead", WorkspaceRoot: root, Title: "Lead", Key: key, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	store := ledger.NewStore(f.registry.WriteDB(), f.registry.ReadDB())
	if err := store.Enable(key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	f.action(t, fmt.Sprintf(`{"action":"coordinator-new","surface":"claude","cwd":%q,"name":"captain"}`, f.host.Cwd))
	f.action(t, `{"action":"coordinator-send","name":"captain","message":"hello","author":"person"}`)
	spec, err := f.registry.CoordinatorNamed("captain")
	if err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the first turn", 15*time.Second, func() bool { return len(f.host.Turns(t, spec.SessionID)) > 0 })
	turn := f.host.Turns(t, spec.SessionID)[0]

	coordinator, err := f.registry.Session(spec.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	journalSource(f.daemon, *coordinator, nil).append(liveEvent(typedPrompt(turn.UUID, "first", turn.Text, time.Now())))
	handler := f.daemon.dashboardHandler(&dashboardServer{token: "test-token"})
	for _, copy := range []ledgerCaptureRequest{
		{SessionID: spec.SessionID, PromptID: "first", Prompt: turn.Text},
		{SessionID: spec.SessionID, PromptID: "first", Prompt: turn.Text, MessageUUID: turn.UUID},
	} {
		if resp := ledgerPost(t, handler, copy); resp.Ref != ledger.DeliveryRef(turn.UUID) {
			t.Fatalf("a hook copy of the first turn named %q, want the delivery %s", resp.Ref, turn.UUID)
		}
	}
	events, err := store.Events(key, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, e := range withoutCapture(events) {
		var u ledger.UtterancePayload
		if err := json.Unmarshal(e.Payload, &u); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, u.Text)
	}
	if len(texts) != 1 || texts[0] != "hello" {
		t.Fatalf("captured %q, want the person's words once", texts)
	}
}
