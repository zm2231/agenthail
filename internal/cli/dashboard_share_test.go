package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/tailscale"
)

func TestDashboardQRIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "share.png")
	if err := writeDashboardQR(tailscale.URL("agent.tailnet.ts.net", 7412), path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || info.Size() == 0 {
		t.Fatalf("mode=%o size=%d", info.Mode().Perm(), info.Size())
	}
}

func TestDashboardRemoteEnabledRequiresHTTPS(t *testing.T) {
	config := daemon.DashboardConfig{Enabled: true}
	var serve tailscale.Serve
	if err := json.Unmarshal([]byte(`{"Web":{"agent.tailnet.ts.net:7412":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7412"}}}}}`), &serve); err != nil {
		t.Fatal(err)
	}
	if dashboardRemoteEnabled(config, serve, "agent.tailnet.ts.net", "http://127.0.0.1:7412", false) {
		t.Fatal("HTTP-only route reported enabled")
	}
	if !dashboardRemoteEnabled(config, serve, "agent.tailnet.ts.net", "http://127.0.0.1:7412", true) {
		t.Fatal("HTTPS route reported disabled")
	}
}
