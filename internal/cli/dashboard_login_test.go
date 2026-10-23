package cli

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/testkit"
)

const dashboardTestSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// dashboardLoginFixture is a running daemon as the CLI sees it: a dashboard
// token on disk, a pid file naming a daemon process, a dashboard API that
// issues login links, and a Tailscale that already serves it.
type dashboardLoginFixture struct {
	app       *App
	tailscale string
	opened    string
	links     atomic.Int32
}

func newDashboardLoginFixture(t *testing.T) *dashboardLoginFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	state := filepath.Join(home, ".agenthail")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(daemon.DashboardTokenPath(), []byte(dashboardTestSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := &dashboardLoginFixture{app: &App{daemonService: &fakeService{loaded: true}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/dashboard/login-links" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+dashboardTestSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var request struct {
			Remote bool `json:"remote"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		base := "http://" + r.Host + "/"
		if request.Remote {
			base = "https://agent.tailnet.ts.net:7412/"
		}
		n := fixture.links.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"url": base + "?login=single-use-" + strconv.Itoa(int(n)), "expiresAt": time.Now().Add(2 * time.Minute)})
	}))
	t.Cleanup(server.Close)
	listen := strings.TrimPrefix(server.URL, "http://")
	if err := daemon.SaveDashboardConfig(daemon.DashboardConfig{Enabled: true, Listen: listen, CodexRecentHours: 5}); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	fakeDaemon := filepath.Join(bin, "agenthail")
	testkit.WriteExecutable(t, fakeDaemon, "#!/bin/sh\nwhile :; do sleep 1; done\n")
	process := exec.Command(fakeDaemon, "daemon-run")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(process.Process.Pid, syscall.SIGKILL); _ = process.Wait() })
	if err := os.WriteFile(daemon.PidFilePath(), []byte(strconv.Itoa(process.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.opened = filepath.Join(bin, "opened")
	testkit.WriteExecutable(t, filepath.Join(bin, "open"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+fixture.opened+"\"\n")
	testkit.WriteExecutable(t, filepath.Join(bin, "pbcopy"), "#!/bin/sh\ncat >/dev/null\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, port, _ := net.SplitHostPort(listen)
	target := "http://127.0.0.1:" + port
	fixture.tailscale = filepath.Join(bin, "tailscale")
	testkit.WriteExecutable(t, fixture.tailscale, `#!/bin/sh
case "$*" in
"status --json") echo '{"BackendState":"Running","Self":{"DNSName":"agent.tailnet.ts.net.","Online":true}}' ;;
"serve status --json") echo '{"TCP":{"7412":{"HTTPS":true}},"Web":{"agent.tailnet.ts.net:7412":{"Handlers":{"/":{"Proxy":"`+target+`"}}}}}' ;;
"serve status") echo 'https://agent.tailnet.ts.net:7412 (tailnet only)' ;;
*) echo '{}' ;;
esac
`)
	return fixture
}

func TestNoDashboardCommandPrintsTheDashboardToken(t *testing.T) {
	fixture := newDashboardLoginFixture(t)
	commands := [][]string{
		{"dashboard"},
		{"dashboard", "status"},
		{"dashboard", "enable"},
		{"dashboard", "remote", "--no-open", "--tailscale", fixture.tailscale},
		{"dashboard", "remote", "on", "--no-open", "--tailscale", fixture.tailscale},
		{"dashboard", "remote", "--json", "--tailscale", fixture.tailscale},
		{"dashboard", "remote", "status", "--tailscale", fixture.tailscale},
		{"dashboard", "remote", "status", "--json", "--tailscale", fixture.tailscale},
	}
	for _, command := range commands {
		_ = os.Remove(fixture.opened)
		output, err := captureStdout(t, func() error { return fixture.app.Run(command) })
		if err != nil {
			t.Fatalf("%v: %v\n%s", command, err, output)
		}
		opened, _ := os.ReadFile(fixture.opened)
		shown := output + string(opened)
		if strings.Contains(shown, dashboardTestSecret) {
			t.Fatalf("%v showed the dashboard token:\n%s", command, shown)
		}
		if command[len(command)-1] != "status" && !strings.Contains(shown, "http") {
			t.Fatalf("%v neither printed nor opened an address:\n%s", command, shown)
		}
	}
	if fixture.links.Load() == 0 {
		t.Fatal("no command asked the daemon for a login link")
	}
	qr, err := os.ReadFile(dashboardShareQRPath())
	if err != nil || strings.Contains(string(qr), dashboardTestSecret) {
		t.Fatalf("QR file: %v", err)
	}
}

func TestDashboardTokenRotateReplacesTheToken(t *testing.T) {
	fixture := newDashboardLoginFixture(t)
	output, err := captureStdout(t, func() error { return fixture.app.Run([]string{"dashboard", "token", "rotate"}) })
	if err != nil {
		t.Fatalf("rotate: %v\n%s", err, output)
	}
	token, err := os.ReadFile(daemon.DashboardTokenPath())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(daemon.DashboardTokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(token) == dashboardTestSecret || len(token) < 32 || info.Mode().Perm() != 0o600 || strings.Contains(output, string(token)) {
		t.Fatalf("rotated token len=%d mode=%o; output %q", len(token), info.Mode().Perm(), output)
	}
	if !strings.Contains(output, "restarted") {
		t.Fatalf("rotating with the daemon running did not restart it: %q", output)
	}
}
