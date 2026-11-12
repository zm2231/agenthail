package daemon

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/simhost"
	"github.com/zm2231/agenthail/internal/testkit"
)

type forwardedSims struct {
	linux, mac *federationNode
	commands   *simCommands
	macHost    *simhost.Host
	record     string
}

func simulatorsOnTwoHosts(t *testing.T) *forwardedSims {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	commands := &simCommands{shotPNG: []byte("\x89PNG\r\n\x1a\nforwarded")}
	mac := newFederationNode(t, "remote-a")
	macHost := simhost.New(simhost.Config{Exec: commands.exec, OutRoot: t.TempDir(), LockDir: t.TempDir(), GOOS: "darwin"})
	mac.daemon.SetSimulatorHost(macHost)
	mac.serve(t, nil)
	linux := newFederationNode(t, "local-a")
	linux.daemon.SetSimulatorHost(simhost.New(simhost.Config{OutRoot: t.TempDir(), GOOS: "linux"}))
	linux.start(t, testRoots(mac.server))
	record := linux.pair(t, mac, "mac")
	host, _ := linux.daemon.pairedHost(record.ID)
	testkit.Eventually(t, "the paired Mac reports its simulators", 10*time.Second, func() bool { return host.runsSimulators() })
	return &forwardedSims{linux: linux, mac: mac, commands: commands, macHost: macHost, record: record.ID}
}

func TestAHostWithoutSimulatorsDrivesAPairedMacs(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	ctx := context.Background()
	out, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "acquire", Kind: "iphone"})
	if err != nil {
		t.Fatal(err)
	}
	if out["host"] != "mac" || out["lease"].(map[string]any)["outDir"] != nil {
		t.Fatalf("forwarded acquire = %v", out)
	}
	owner := hosts.Qualify(f.linux.daemon.localHost().ID, "local-a")
	if !f.macHost.Holds(owner) || f.macHost.Holds("local-a") {
		t.Fatal("the Mac does not hold the lease for the forwarding host's session, qualified by that host")
	}
	shot, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "screenshot"})
	if err != nil {
		t.Fatal(err)
	}
	file, _ := shot["file"].(string)
	if data, err := os.ReadFile(file); err != nil || string(data) != string(f.commands.shotPNG) {
		t.Fatalf("the screenshot was not written on the forwarding host: %v %q (%v)", err, data, shot)
	}
	if _, raw := shot["fileData"]; raw || !strings.HasSuffix(file, "-screenshot.png") || strings.Count(filepath.Base(file), "-") != 1 {
		t.Fatalf("the forwarded result %v kept the Mac's encoding or name", shot)
	}
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "install", App: "/tmp/App.app"}); refusalOf(err) != "unavailable" {
		t.Fatalf("forwarded install = %v", err)
	}
	f.linux.daemon.releaseForwardedSimulators("local-a")
	if f.macHost.Holds(owner) {
		t.Fatal("archiving on the forwarding host left the Mac's lease held")
	}
}

func TestOnlyAPairedHostForwardsSimulatorCalls(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	response := f.mac.request(t, http.MethodPost, "/api/v1/simulator", simulatorForwardRequest{Owner: "local-a", Request: simhost.Request{Action: "acquire", Kind: "iphone"}})
	if response.Code == http.StatusOK {
		t.Fatalf("a client that is not a paired host forwarded a call: %d %s", response.Code, response.Body)
	}
	if f.macHost.Holds("local-a") || f.commands.ran("xcrun simctl boot") {
		t.Fatal("the refused call reached the simulator")
	}
}

func TestSimulatorHostChoiceIsConfig(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	if err := SaveDashboardConfig(DashboardConfig{SimulatorHost: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.linux.daemon.forwardSimulator(context.Background(), "local-a", simhost.Request{Action: "list"}); refusalOf(err) != "unavailable" || !strings.Contains(err.Error(), "elsewhere") {
		t.Fatalf("a simulatorHost that names no paired host = %v", err)
	}
	if err := SaveDashboardConfig(DashboardConfig{SimulatorHost: "mac"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.linux.daemon.forwardSimulator(context.Background(), "local-a", simhost.Request{Action: "list"}); err != nil {
		t.Fatalf("simulatorHost naming the Mac = %v", err)
	}
}

func refusalOf(err error) string {
	var refusal *mcpRefusal
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return ""
}
