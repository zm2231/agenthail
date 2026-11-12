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

type simMac struct {
	node     *federationNode
	commands *simCommands
	host     *simhost.Host
}

type forwardedSims struct {
	linux, mac *federationNode
	commands   *simCommands
	macHost    *simhost.Host
	macs       map[string]*simMac
}

func simulatorsOnHosts(t *testing.T, names ...string) *forwardedSims {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	linux := newFederationNode(t, "local-a")
	linux.daemon.SetSimulatorHost(simhost.New(simhost.Config{OutRoot: t.TempDir(), GOOS: "linux"}))
	macs := map[string]*simMac{}
	var servers []*federationNode
	for _, name := range names {
		commands := &simCommands{shotPNG: []byte("\x89PNG\r\n\x1a\nforwarded")}
		node := newFederationNode(t, "remote-"+name)
		host := simhost.New(simhost.Config{Exec: commands.exec, OutRoot: t.TempDir(), LockDir: t.TempDir(), GOOS: "darwin"})
		node.daemon.SetSimulatorHost(host)
		node.serve(t, nil)
		macs[name] = &simMac{node: node, commands: commands, host: host}
		servers = append(servers, node)
	}
	linux.start(t, testRoots(servers[0].server))
	for _, name := range names {
		record := linux.pair(t, macs[name].node, name)
		host, _ := linux.daemon.pairedHost(record.ID)
		testkit.Eventually(t, name+" reports its simulators", 10*time.Second, func() bool { return host.runsSimulators() })
	}
	first := macs[names[0]]
	return &forwardedSims{linux: linux, mac: first.node, commands: first.commands, macHost: first.host, macs: macs}
}

func simulatorsOnTwoHosts(t *testing.T) *forwardedSims {
	t.Helper()
	return simulatorsOnHosts(t, "mac")
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

func TestArchivingReleasesEveryMacTheSessionCalled(t *testing.T) {
	f := simulatorsOnHosts(t, "mac", "studio")
	ctx := context.Background()
	if err := SaveDashboardConfig(DashboardConfig{SimulatorHost: "mac"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "acquire", Kind: "iphone"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveDashboardConfig(DashboardConfig{SimulatorHost: "studio"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "acquire", Kind: "iphone"}); err != nil {
		t.Fatal(err)
	}
	owner := hosts.Qualify(f.linux.daemon.localHost().ID, "local-a")
	if !f.macs["mac"].host.Holds(owner) || !f.macs["studio"].host.Holds(owner) {
		t.Fatal("the session does not hold a lease on both Macs")
	}
	f.linux.daemon.releaseForwardedSimulators("local-a")
	for name, mac := range f.macs {
		if mac.host.Holds(owner) {
			t.Fatalf("archiving left the lease on %s held", name)
		}
	}
}

func TestAMacThatGainsAXeBecomesSelectableWithoutReconnecting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := simulatorsOnTwoHosts(t)
	f.commands.mu.Lock()
	f.commands.noAXe = true
	f.commands.mu.Unlock()
	age := func() {
		f.mac.daemon.simStatusMu.Lock()
		f.mac.daemon.simStatusAt = time.Now().Add(-2 * time.Minute)
		f.mac.daemon.simStatusMu.Unlock()
		for _, host := range f.linux.daemon.pairedHosts() {
			host.mu.Lock()
			host.simsAt = time.Now().Add(-2 * time.Minute)
			host.mu.Unlock()
		}
	}
	age()
	f.mac.daemon.simulatorStatus(context.Background())
	age()
	if _, err := f.linux.daemon.forwardSimulator(context.Background(), "local-a", simhost.Request{Action: "list"}); refusalOf(err) != "unavailable" {
		t.Fatalf("list while the Mac lacks AXe = %v", err)
	}
	f.commands.mu.Lock()
	f.commands.noAXe = false
	f.commands.mu.Unlock()
	age()
	f.mac.daemon.simulatorStatus(context.Background())
	age()
	testkit.Eventually(t, "the Mac becomes selectable once it has AXe", 10*time.Second, func() bool {
		_, err := f.linux.daemon.forwardSimulator(context.Background(), "local-a", simhost.Request{Action: "list"})
		return err == nil
	})
}

func TestAnUnreadableConfigPicksNoMac(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	if err := os.MkdirAll(filepath.Dir(DashboardConfigPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DashboardConfigPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := f.linux.daemon.forwardSimulator(context.Background(), "local-a", simhost.Request{Action: "list"})
	if refusalOf(err) != "unavailable" || !strings.Contains(err.Error(), "can't be read") {
		t.Fatalf("a call with an unreadable dashboard.json = %v", err)
	}
	if f.commands.ran("xcrun simctl list devices") {
		t.Fatal("the call reached the Mac")
	}
}

func TestArchivingWaitsForAnAcquireStillOnItsWay(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	f.commands.hold("xcrun simctl boot")
	acquired := make(chan error, 1)
	go func() {
		_, err := f.linux.daemon.callSimulator(context.Background(), "local-a", simhost.Request{Action: "acquire", Kind: "iphone"})
		acquired <- err
	}()
	<-f.commands.held
	f.linux.daemon.releaseSimulators("local-a")
	time.Sleep(100 * time.Millisecond)
	close(f.commands.release)
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
	owner := hosts.Qualify(f.linux.daemon.localHost().ID, "local-a")
	testkit.Eventually(t, "the archived session's lease is released on the Mac", 10*time.Second, func() bool { return !f.macHost.Holds(owner) })
}

func TestArchivingWaitsForALocalAcquireStillOnItsWay(t *testing.T) {
	commands := &simCommands{}
	f := newSimFixture(t, commands)
	commands.hold("xcrun simctl boot")
	acquired := make(chan error, 1)
	go func() {
		_, err := f.d.callSimulator(context.Background(), f.parentID, simhost.Request{Action: "acquire", Kind: "iphone"})
		acquired <- err
	}()
	<-commands.held
	f.d.releaseSimulators(f.parentID)
	time.Sleep(100 * time.Millisecond)
	close(commands.release)
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the archived session's lease is released", 10*time.Second, func() bool { return !f.d.simHost.Holds(f.parentID) })
}

func TestARestoredSessionsNextCallWaitsForTheArchiveRelease(t *testing.T) {
	commands := &simCommands{}
	f := newSimFixture(t, commands)
	if _, err := f.d.callSimulator(context.Background(), f.parentID, simhost.Request{Action: "acquire", Kind: "iphone"}); err != nil {
		t.Fatal(err)
	}
	commands.hold("xcrun simctl location")
	f.d.releaseSimulators(f.parentID)
	<-commands.held
	called := make(chan error, 1)
	go func() {
		_, err := f.d.callSimulator(context.Background(), f.parentID, simhost.Request{Action: "button", Button: "home"})
		called <- err
	}()
	select {
	case err := <-called:
		t.Fatalf("the restored session's call ran while the release was still going: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if commands.ran("axe button") {
		t.Fatal("the restored session's call reached the simulator before the release finished")
	}
	close(commands.release)
	if err := <-called; err == nil || !strings.Contains(err.Error(), "acquire") {
		t.Fatalf("after the release the call = %v, want it to find no lease", err)
	}
	testkit.Eventually(t, "the session's simulator lock is reclaimed", 10*time.Second, func() bool {
		f.d.simOwnersMu.Lock()
		defer f.d.simOwnersMu.Unlock()
		return len(f.d.simOwners) == 0
	})
}
