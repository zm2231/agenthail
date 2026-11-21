package surfaces

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func desktopAnsweringDiagnostics(t *testing.T, process string) string {
	t.Helper()
	return startDesktopBridge(t, func(method string) string {
		switch method {
		case "thread/loaded/list":
			return `{"result":{"data":[]}}`
		case "server/diagnostics":
			if process == "" {
				return `{"error":{"code":-32601,"message":"unknown method"}}`
			}
			return `{"result":{"gauges":[],"process":{"id":` + process + `}}}`
		}
		return ""
	}).URL
}

func TestCodexDoctorTellsWhetherDesktopSharesTheDaemon(t *testing.T) {
	for _, test := range []struct {
		name    string
		desktop string
		daemon  int
		want    surface.AppServerMode
	}{
		{name: "one process answers both", desktop: "4242", daemon: 4242, want: surface.AppServerShared},
		{name: "Desktop runs its own app-server", desktop: "1111", daemon: 4242, want: surface.AppServerPrivate},
		{name: "no daemon is running", desktop: "1111", want: surface.AppServerPrivate},
		{name: "Desktop cannot name its process", daemon: 4242},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.daemon > 0 {
				startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
					if method == "server/diagnostics" {
						return map[string]any{"gauges": []any{}, "process": map[string]any{"id": test.daemon}}
					}
					return nil
				})
			} else {
				t.Setenv("CODEX_HOME", t.TempDir())
			}
			codex := inDaemon(NewCodex(desktopAnsweringDiagnostics(t, test.desktop)))
			codex.managed = true
			status := codex.RuntimeStatus(context.Background())
			if !status.Reachable || status.Backend != "desktop" {
				t.Fatalf("status=%+v", status)
			}
			var got surface.AppServerMode
			if status.AppServer != nil {
				got = status.AppServer.Mode
			}
			if got != test.want {
				t.Fatalf("mode=%q want %q", got, test.want)
			}
		})
	}
}
