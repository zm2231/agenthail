//go:build gateway_live

package daemon

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

// TestZENLiveCodexGateway exercises the Go HTTP gateway with the ZEN TypeScript
// transport against a dedicated native Codex task. It never starts the installed daemon.
func TestZENLiveCodexGateway(t *testing.T) {
	threadID := os.Getenv("AGENTHAIL_ZEN_LIVE_THREAD_ID")
	zenRoot := os.Getenv("AGENTHAIL_ZEN_LIVE_ROOT")
	if threadID == "" || !filepath.IsAbs(zenRoot) {
		t.Skip("requires dedicated thread ID and absolute ZEN checkout")
	}
	reg, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	codex := surfaces.NewCodex("")
	listCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	sessions, err := codex.List(listCtx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var target *surface.Session
	for i := range sessions {
		if sessions[i].ID == threadID {
			target = &sessions[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("dedicated Codex task %s was not discovered", threadID)
	}
	if err := reg.RegisterSession(*target); err != nil {
		t.Fatal(err)
	}
	d := New(reg, []surface.Surface{codex})
	token := uuid.NewString()
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: token}))
	defer server.Close()
	marker := "gateway-live-" + uuid.NewString()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	script := `
const { AgenthailTransport } = await import(process.env.ZEN_TRANSPORT_MODULE);
const { AgenthailExternalHarnessAdapter } = await import(process.env.ZEN_ADAPTER_MODULE);
const id = process.env.GATEWAY_THREAD_ID;
const marker = process.env.GATEWAY_MARKER;
const transport = new AgenthailTransport(process.env.GATEWAY_URL, process.env.GATEWAY_TOKEN);
const adapter = new AgenthailExternalHarnessAdapter(transport);
const state = await adapter.inspect(id);
if (state.state === 'orphaned' || state.managementCapabilities?.send !== true || state.managementCapabilities?.stream !== true) throw new Error('native session is not writable and streamable: ' + JSON.stringify(state));
const observed = (async () => {
  for await (const item of adapter.streamEventsWithCursor(id)) {
    if (item.event.type === 'message' && item.event.role === 'assistant' && item.event.text.includes(marker)) return item;
  }
  throw new Error('stream closed before the native reply');
})();
await new Promise(resolve => setTimeout(resolve, 250));
const prompt = 'This is a live gateway integration check. Please acknowledge marker ' + marker + ' naturally in one sentence.';
const key = 'gateway-check:' + marker;
const receipt = await transport.command(id, 'interact', prompt, undefined, key);
const replay = await transport.command(id, 'interact', prompt, undefined, key);
if (JSON.stringify(receipt) !== JSON.stringify(replay)) throw new Error('idempotency replay changed receipt');
if (receipt.disposition === 'failed' || receipt.disposition === 'unknown') throw new Error('native delivery was not confirmed: ' + JSON.stringify(receipt));
const item = await observed;
console.log(JSON.stringify({receipt, replay, cursor:item.cursor, event:item.event}));
`
	command := exec.CommandContext(ctx, "bun", "-e", script)
	command.Env = append(os.Environ(),
		"ZEN_TRANSPORT_MODULE="+filepath.Join(zenRoot, "packages/runtimes/src/agenthail-transport.ts"),
		"ZEN_ADAPTER_MODULE="+filepath.Join(zenRoot, "packages/runtimes/src/harnesses/agenthail-external.ts"),
		"GATEWAY_THREAD_ID="+threadID,
		"GATEWAY_MARKER="+marker,
		"GATEWAY_URL="+server.URL,
		"GATEWAY_TOKEN="+token,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("ZEN cross-process gateway check: %v: %s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), marker) || !strings.Contains(string(output), `"cursor"`) {
		t.Fatal(fmt.Errorf("native reply or cursor missing: %s", output))
	}
	t.Logf("ZEN cross-process native Codex reply: %s", strings.TrimSpace(string(output)))
}
