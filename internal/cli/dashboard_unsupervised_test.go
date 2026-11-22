package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemonctl"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/testkit"
)

// unsupervisedHost is a Linux host with no reachable systemd user manager.
type unsupervisedHost struct {
	app       *App
	systemctl *fakeSystemctl
	tailscale string
}

func newUnsupervisedHost(t *testing.T, daemonScript string) *unsupervisedHost {
	t.Helper()
	systemctl, service := newFakeSystemctl(t)
	systemctl.managerDown = true
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+dashboardTestSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/host":
			_ = json.NewEncoder(w).Encode(map[string]any{"host": map[string]string{"id": "host-1", "name": "vm"}})
		case "/api/v1/extensions":
			_ = json.NewEncoder(w).Encode(map[string]any{"host": "running", "extensions": []any{}})
		case "/api/v1/dashboard/login-links":
			_ = json.NewEncoder(w).Encode(map[string]any{"url": "https://vm.tailnet.ts.net:7412/?login=once", "expiresAt": time.Now().Add(time.Minute)})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(api.Close)
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".agenthail"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dashconfig.TokenPath(), []byte(dashboardTestSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dashconfig.Save(dashconfig.Config{Enabled: false, Listen: strings.TrimPrefix(api.URL, "http://"), CodexRecentHours: 5}); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	daemon := filepath.Join(bin, "agenthail")
	testkit.WriteExecutable(t, daemon, daemonScript)
	unsupervisedDaemonExecutable = func() (string, error) { return daemon, nil }
	t.Cleanup(func() { unsupervisedDaemonExecutable = os.Executable })
	t.Cleanup(func() {
		if _, running := daemonctl.IsRunning(); running {
			_ = daemonctl.Stop()
		}
	})
	host := &unsupervisedHost{app: &App{daemonService: service}, systemctl: systemctl, tailscale: filepath.Join(bin, "tailscale")}
	served := filepath.Join(bin, "served")
	testkit.WriteExecutable(t, host.tailscale, `#!/bin/sh
case "$*" in
"status --json") echo '{"BackendState":"Running","Self":{"DNSName":"vm.tailnet.ts.net.","Online":true}}' ;;
"serve status --json") if [ -f `+served+` ]; then echo "{\"TCP\":{\"7412\":{\"HTTPS\":true}},\"Web\":{\"vm.tailnet.ts.net:7412\":{\"Handlers\":{\"/\":{\"Proxy\":\"$(cat `+served+`)\"}}}}}"; else echo '{}'; fi ;;
"serve status") if [ -f `+served+` ]; then echo 'https://vm.tailnet.ts.net:7412 (tailnet only)'; else echo 'No serve config'; fi ;;
"serve --bg --yes --https=7412 "*) echo "$5" > `+served+` ;;
*) exit 1 ;;
esac
`)
	testkit.WriteExecutable(t, filepath.Join(bin, "pbcopy"), "#!/bin/sh\ncat >/dev/null\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return host
}

const runningDaemonScript = "#!/bin/sh\necho $$ > \"$HOME/.agenthail/daemon.pid\"\ntrap 'exit 0' TERM\n"

func TestDashboardRemoteServesAnUnsupervisedDaemonWithoutAUserManager(t *testing.T) {
	host := newUnsupervisedHost(t, runningDaemonScript+testkit.WhileTestRuns("0.05"))
	if out, err := captureStdout(t, host.app.daemonStart); err != nil {
		t.Fatalf("daemon start: %v\n%s", err, out)
	}
	before, _ := daemonctl.IsRunning()

	out, err := captureStdout(t, func() error {
		return host.app.Run([]string{"dashboard", "remote", "--no-open", "--tailscale", host.tailscale})
	})
	if err != nil {
		t.Fatalf("dashboard remote: %v\n%s", err, out)
	}
	if after, running := daemonctl.IsRunning(); !running || after == before {
		t.Fatalf("the unsupervised daemon was not restarted to serve the dashboard: before %d, after %d, running %v", before, after, running)
	}
	if _, err := os.Stat(host.systemctl.unitPath); !os.IsNotExist(err) {
		t.Fatalf("a systemd unit was written for a manager that is unreachable: %v", err)
	}
	config, err := dashconfig.Load()
	if err != nil || !config.Enabled || !config.RemoteAccess.Enabled {
		t.Fatalf("dashboard config after remote: %+v, %v", config, err)
	}
	named, err := captureStdout(t, func() error { return host.app.Run([]string{"hosts", "name"}) })
	if err != nil || !strings.Contains(named, "vm (host-1)") {
		t.Fatalf("hosts name: %v\n%s", err, named)
	}
}

func TestDashboardRemoteThatCannotStartTheDaemonSaysWhatItEnabled(t *testing.T) {
	host := newUnsupervisedHost(t, "#!/bin/sh\nexit 1\n")

	out, err := captureStdout(t, func() error {
		return host.app.Run([]string{"dashboard", "remote", "--no-open", "--tailscale", host.tailscale})
	})
	if err == nil {
		t.Fatalf("dashboard remote succeeded with a daemon that cannot start\n%s", out)
	}
	if !strings.Contains(err.Error(), "the dashboard is now enabled in "+dashconfig.Path()) || !strings.Contains(err.Error(), "run 'agenthail dashboard remote' again") {
		t.Fatalf("the failure does not say what it saved and how to finish: %v", err)
	}
	if config, loadErr := dashconfig.Load(); loadErr != nil || !config.Enabled || config.RemoteAccess.Enabled {
		t.Fatalf("config after the failure: %+v, %v", config, loadErr)
	}
}

func TestDaemonStartOnAFreshHomeCreatesItsStateDirectory(t *testing.T) {
	systemctl, service := newFakeSystemctl(t)
	systemctl.managerDown = true
	bin := filepath.Join(t.TempDir(), "agenthail")
	testkit.WriteExecutable(t, bin, runningDaemonScript+testkit.WhileTestRuns("0.05"))
	unsupervisedDaemonExecutable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { unsupervisedDaemonExecutable = os.Executable })
	t.Cleanup(func() {
		if _, running := daemonctl.IsRunning(); running {
			_ = daemonctl.Stop()
		}
	})

	out, err := captureStdout(t, (&App{daemonService: service}).daemonStart)
	if err != nil {
		t.Fatalf("daemon start on a fresh HOME: %v\n%s", err, out)
	}
	info, err := os.Stat(filepath.Dir(daemonctl.LogFilePath()))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("state directory: %v, %v", info, err)
	}
}
