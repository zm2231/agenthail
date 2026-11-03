package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
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

func TestACreatedClaudeHostSessionReachesTheMCPServerAsItself(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudehosttest.New(t, "")
	fixture.Start(t)
	claude := surfaces.NewClaude(nil, fixture.Home)
	r := hostRegistry(t)
	d := newTestDaemon(t, r, []surface.Surface{claude})
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: dashboardTestToken}))
	t.Cleanup(server.Close)
	d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	w := serveDashboardRequest(dashboardRouter(d), http.MethodPost, "/api/action", fmt.Sprintf(`{"action":"session-create","surface":"claude","message":"hello","cwd":%q}`, fixture.Cwd))
	var created map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code >= 300 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	spawns := fixture.Spawns(t, id)
	if id == "" || len(spawns) != 1 {
		t.Fatalf("id=%q spawns=%d body=%s", id, len(spawns), w.Body)
	}
	token := spawnBearer(t, spawns[0])
	if whoami, refused := callMCP(t, mcpSession(t, server, token), "agenthail_whoami", nil); refused || whoami["sessionId"] != id {
		t.Fatalf("whoami=%v refused=%v", whoami, refused)
	}
}
