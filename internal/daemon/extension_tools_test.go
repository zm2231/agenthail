package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/poll"
)

const toolServer = `
export default function (ah: any) {
  ah.tool({
    name: 'peek',
    description: 'What the calling session sees through this extension',
    inputSchema: { type: 'object', properties: { note: { type: 'string' } } },
    run: async (input: any, ctx: any) => {
      const sessions = (await ctx.ah.call('agenthail_sessions', {})).sessions.map((s: any) => s.id);
      let create = 'allowed';
      try {
        await ctx.ah.call('agenthail_create', { surface: 'claude', message: 'hi' });
      } catch (error: any) {
        create = error.code;
      }
      return { caller: ctx.caller, note: input.note ?? '', sessions, create };
    },
  });
}
`

func writeToolExtension(t *testing.T, home, id, manifest, server string) {
	t.Helper()
	dir := filepath.Join(extension.Workspace(home), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"extension.json": manifest, "server.ts": server} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *extensionFixture) approve(t *testing.T, id string) {
	t.Helper()
	if response, body := f.request(t, http.MethodPost, "/api/v1/extensions", "secret", map[string]string{"action": "approve", "id": id}); response.StatusCode != http.StatusOK {
		t.Fatalf("approve %s answered %d %s", id, response.StatusCode, body)
	}
}

func (f *extensionFixture) awaitAllLoaded(t *testing.T, ids ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var last extensionsView
	if err := poll.Until(ctx, 20*time.Millisecond, func() (bool, error) {
		last = f.d.extensionsView()
		loaded := 0
		for _, ext := range last.Extensions {
			if slices.Contains(ids, ext.ID) && ext.State == "loaded" {
				loaded++
			}
		}
		return last.Host == "running" && loaded == len(ids), nil
	}); err != nil {
		t.Fatalf("%v never all loaded: %+v", ids, last)
	}
}

func listedToolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	listed, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestAnAgentSessionCallsAnExtensionToolWithOnlyWhatBothMayDo(t *testing.T) {
	home := extensionTestHome(t)
	writeToolExtension(t, home, "narrow", `{"label": "Narrow", "capabilities": ["tools"]}`, toolServer)
	writeToolExtension(t, home, "wide", `{"label": "Wide", "capabilities": ["tools", "read.all"]}`, toolServer)
	f := startExtensionDaemon(t, home, extensionDaemonOptions{})
	f.awaitAllLoaded(t, "narrow", "wide")
	from, err := f.d.Registry.Session("from")
	if err != nil {
		t.Fatal(err)
	}
	session := mcpSession(t, f.server, issueMCPToken(t, f.d, *from))
	if names := listedToolNames(t, session); slices.ContainsFunc(names, func(name string) bool { return strings.HasPrefix(name, "ext_") }) {
		t.Fatalf("tools were offered before the person approved them: %v", names)
	}

	f.approve(t, "narrow")
	f.approve(t, "wide")
	names := listedToolNames(t, session)
	if !slices.Contains(names, "ext_narrow_peek") || !slices.Contains(names, "ext_wide_peek") || !slices.Contains(names, "agenthail_send") {
		t.Fatalf("after approval the session lists %v", names)
	}

	wide, failed := callMCP(t, session, "ext_wide_peek", map[string]any{"note": "hello"})
	if failed {
		t.Fatalf("ext_wide_peek refused: %v", wide)
	}
	caller, _ := wide["caller"].(map[string]any)
	if caller["kind"] != "session" || caller["sessionId"] != f.d.publicSessionID("from") || wide["note"] != "hello" {
		t.Fatalf("the tool saw %v", wide)
	}
	if sessions := fmt.Sprint(wide["sessions"]); !strings.Contains(sessions, "from") {
		t.Fatalf("with read.all approved the tool should read what the session reads, got %s", sessions)
	}
	if wide["create"] != "not_for_extensions" {
		t.Fatalf("a tool spawned a session as its caller: %v", wide["create"])
	}

	narrow, failed := callMCP(t, session, "ext_narrow_peek", nil)
	if failed {
		t.Fatalf("ext_narrow_peek refused: %v", narrow)
	}
	if sessions, _ := narrow["sessions"].([]any); len(sessions) != 0 {
		t.Fatalf("an extension without read.all read the caller's sessions: %v", sessions)
	}

	loaded, _ := f.d.extensions.find("wide")
	if names := listedToolNames(t, mcpSession(t, f.server, loaded.token)); slices.Contains(names, "ext_wide_peek") {
		t.Fatalf("an extension was offered extension tools: %v", names)
	}
}

func TestExtensionToolsPastTheBudgetAreNotOfferedAndSaySo(t *testing.T) {
	home := extensionTestHome(t)
	var tools strings.Builder
	for i := range 5 {
		fmt.Fprintf(&tools, "ah.tool({name: 't%d', description: %q, run: () => null});\n", i, strings.Repeat("x", 1800))
	}
	writeToolExtension(t, home, "a-big", `{"label": "Big", "capabilities": ["tools"]}`, "export default function (ah: any) {\n"+tools.String()+"}\n")
	writeToolExtension(t, home, "b-small", `{"label": "Small", "capabilities": ["tools"]}`, toolServer)
	f := startExtensionDaemon(t, home, extensionDaemonOptions{})
	f.awaitAllLoaded(t, "a-big", "b-small")
	f.approve(t, "a-big")
	f.approve(t, "b-small")

	from, err := f.d.Registry.Session("from")
	if err != nil {
		t.Fatal(err)
	}
	names := listedToolNames(t, mcpSession(t, f.server, issueMCPToken(t, f.d, *from)))
	if slices.ContainsFunc(names, func(name string) bool { return strings.HasPrefix(name, "ext_a-big_") }) || !slices.Contains(names, "ext_b-small_peek") {
		t.Fatalf("listed %v", names)
	}
	for _, ext := range f.d.extensionsView().Extensions {
		if ext.ID == "a-big" && (len(ext.Tools) != 0 || !strings.Contains(ext.ToolsNote, "budget")) {
			t.Fatalf("the over-budget extension shows %+v", ext)
		}
	}
}
