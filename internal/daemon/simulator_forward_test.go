package daemon

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/simhost"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type simMac struct {
	node     *federationNode
	commands *simCommands
	host     *simhost.Host
	outRoot  string
	received *countingListener
}

type forwardedSims struct {
	linux, mac *federationNode
	commands   *simCommands
	macHost    *simhost.Host
	macOut     string
	received   *countingListener
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
		outRoot := t.TempDir()
		host := simhost.New(simhost.Config{Exec: commands.exec, OutRoot: outRoot, LockDir: t.TempDir(), GOOS: "darwin"})
		node.daemon.SetSimulatorHost(host)
		// The mirror reads simulators from one version per connection, which names none before the first probe ends.
		if !node.daemon.simulatorStatus(context.Background()).Ready {
			t.Fatalf("%s's simulator probe did not report ready", name)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		received := &countingListener{Listener: listener}
		node.serve(t, received)
		macs[name] = &simMac{node: node, commands: commands, host: host, outRoot: outRoot, received: received}
		servers = append(servers, node)
	}
	linux.start(t, testRoots(servers[0].server))
	for _, name := range names {
		record := linux.pair(t, macs[name].node, name)
		host, _ := linux.daemon.pairedHost(record.ID)
		testkit.Eventually(t, name+" reports its simulators", 10*time.Second, func() bool { return host.runsSimulators() })
	}
	first := macs[names[0]]
	return &forwardedSims{linux: linux, mac: first.node, commands: first.commands, macHost: first.host, macOut: first.outRoot, received: first.received, macs: macs}
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
	f.linux.daemon.releaseForwardedSimulators("local-a")
	if f.macHost.Holds(owner) {
		t.Fatal("archiving on the forwarding host left the Mac's lease held")
	}
}

func TestAHostWithoutSimulatorsUninstallsFromAPairedMacsLeasedDevice(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	ctx := context.Background()
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "uninstall", BundleID: "com.example.app"}); refusalOf(err) != "not_held" {
		t.Fatalf("forwarded uninstall without a lease = %v", err)
	}
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "acquire", Kind: "iphone"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-b", simhost.Request{Action: "uninstall", BundleID: "com.example.app"}); refusalOf(err) != "not_held" {
		t.Fatalf("forwarded uninstall by a session that holds no lease = %v", err)
	}
	if f.commands.ran("xcrun simctl uninstall") {
		t.Fatal("a refused uninstall reached the Mac's simulator")
	}
	out, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "uninstall", BundleID: "com.example.app"})
	if err != nil {
		t.Fatal(err)
	}
	if out["host"] != "mac" || out["uninstalled"] != "com.example.app" || !f.commands.ran("xcrun simctl uninstall U1 com.example.app") {
		t.Fatalf("forwarded uninstall = %v", out)
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
	if err := dashconfig.Save(dashconfig.Config{SimulatorHost: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.linux.daemon.forwardSimulator(context.Background(), "local-a", simhost.Request{Action: "list"}); refusalOf(err) != "unavailable" || !strings.Contains(err.Error(), "elsewhere") {
		t.Fatalf("a simulatorHost that names no paired host = %v", err)
	}
	if err := dashconfig.Save(dashconfig.Config{SimulatorHost: "mac"}); err != nil {
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
	if err := dashconfig.Save(dashconfig.Config{SimulatorHost: "mac"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "acquire", Kind: "iphone"}); err != nil {
		t.Fatal(err)
	}
	if err := dashconfig.Save(dashconfig.Config{SimulatorHost: "studio"}); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(dashconfig.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dashconfig.Path(), []byte("{not json"), 0o600); err != nil {
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

func TestTheVersionAnswersWhileASimulatorProbeIsSlow(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	commands := &simCommands{}
	commands.hold("xcrun simctl help")
	d.SetSimulatorHost(simhost.New(simhost.Config{Exec: commands.exec, OutRoot: t.TempDir(), LockDir: t.TempDir(), GOOS: "darwin"}))
	version := func() apiVersionResponse {
		t.Helper()
		answered := make(chan apiVersionResponse, 1)
		go func() {
			response := httptest.NewRecorder()
			d.apiVersionHandler(response, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
			var v apiVersionResponse
			_ = json.Unmarshal(response.Body.Bytes(), &v)
			answered <- v
		}()
		select {
		case v := <-answered:
			return v
		case <-time.After(2 * time.Second):
			t.Fatal("the version waited on the simulator probe")
			return apiVersionResponse{}
		}
	}
	if v := version(); v.Simulators != nil {
		t.Fatalf("simulators before any probe finished = %v", *v.Simulators)
	}
	<-commands.held
	if v := version(); v.Simulators != nil {
		t.Fatalf("simulators while the probe runs = %v", *v.Simulators)
	}
	close(commands.release)
	testkit.Eventually(t, "the finished probe shows in the version", 5*time.Second, func() bool {
		v := version()
		return v.Simulators != nil && *v.Simulators
	})
	d.simStatusMu.Lock()
	d.simStatusAt = time.Now().Add(-2 * time.Minute)
	d.simStatusMu.Unlock()
	commands.hold("xcrun simctl help")
	if v := version(); v.Simulators == nil || !*v.Simulators {
		t.Fatalf("a stale status was dropped while it is reprobed: %v", v.Simulators)
	}
	<-commands.held
	if v := version(); v.Simulators == nil || !*v.Simulators {
		t.Fatalf("simulators while the reprobe runs = %v", v.Simulators)
	}
	close(commands.release)
}

type countingListener struct {
	net.Listener
	read atomic.Int64
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, read: &l.read}, nil
}

type countingConn struct {
	net.Conn
	read *atomic.Int64
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.read.Add(int64(n))
	return n, err
}

func stagedFiles(app string) map[string]string {
	files := map[string]string{}
	_ = filepath.WalkDir(app, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, _ := entry.Info()
		data, _ := os.ReadFile(name)
		rel, _ := filepath.Rel(app, name)
		files[filepath.ToSlash(rel)] = fmt.Sprintf("%o %s", info.Mode().Perm()&0o111, data)
		return nil
	})
	return files
}

func builtApp(t *testing.T, filler int) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "Rune.app")
	if err := os.MkdirAll(filepath.Join(app, "Frameworks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, file := range map[string]struct {
		data string
		mode os.FileMode
	}{"Rune": {"binary", 0o755}, "Info.plist": {"plist", 0o644}, "Frameworks/Kit": {"kit", 0o755}} {
		if err := os.WriteFile(filepath.Join(app, filepath.FromSlash(name)), []byte(file.data), file.mode); err != nil {
			t.Fatal(err)
		}
	}
	if filler > 0 {
		data := make([]byte, filler)
		if _, err := rand.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(app, "assets.car"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

func installCopiesLeft(t *testing.T, outRoot string) []string {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(outRoot, ".staging", "install-*"))
	if err != nil {
		t.Fatal(err)
	}
	return left
}

func TestARemoteSessionInstallsItsAppOnTheMacsLeasedSimulator(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	ctx := context.Background()
	app := builtApp(t, 0)
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "acquire", Kind: "iphone"}); err != nil {
		t.Fatal(err)
	}
	out, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "install", Kind: "iphone", App: app})
	if err != nil {
		t.Fatal(err)
	}
	if out["installed"] != app || out["host"] != "mac" {
		t.Fatalf("forwarded install = %v", out)
	}
	f.commands.mu.Lock()
	staged := f.commands.staged
	f.commands.mu.Unlock()
	want := map[string]string{"Rune": "111 binary", "Info.plist": "0 plist", "Frameworks/Kit": "111 kit"}
	if fmt.Sprint(staged) != fmt.Sprint(want) {
		t.Fatalf("simctl installed %v, want %v", staged, want)
	}
	if left := installCopiesLeft(t, f.macOut); len(left) != 0 {
		t.Fatalf("the Mac kept the uploaded copy: %v", left)
	}
}

func TestARemoteInstallWithoutALeaseSendsNoApp(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	app := builtApp(t, 4<<20)
	before := f.received.read.Load()
	_, err := f.linux.daemon.forwardSimulator(context.Background(), "local-a", simhost.Request{Action: "install", App: app})
	if refusalOf(err) != "not_held" {
		t.Fatalf("install without a lease = %v", err)
	}
	if read := f.received.read.Load() - before; read > 64<<10 {
		t.Fatalf("the Mac received %d bytes for a refused install", read)
	}
	if f.commands.ran("xcrun simctl install") || len(installCopiesLeft(t, f.macOut)) != 0 {
		t.Fatal("a refused install reached the simulator or left a copy")
	}
}

func TestAFailedRemoteInstallLeavesNoCopyOnTheMac(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	ctx := context.Background()
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "acquire", Kind: "iphone"}); err != nil {
		t.Fatal(err)
	}
	f.commands.mu.Lock()
	f.commands.failInstall = true
	f.commands.mu.Unlock()
	_, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "install", App: builtApp(t, 0)})
	if refusalOf(err) != "simulator_failed" || !strings.Contains(err.Error(), "not built for the simulator") {
		t.Fatalf("failed install = %v", err)
	}
	if left := installCopiesLeft(t, f.macOut); len(left) != 0 {
		t.Fatalf("a failed install left %v", left)
	}
}

func TestAnOversizedAppIsRefusedBeforeUpload(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	ctx := context.Background()
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "acquire", Kind: "iphone"}); err != nil {
		t.Fatal(err)
	}
	app := builtApp(t, 0)
	sparse, err := os.Create(filepath.Join(app, "huge.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sparse.Truncate(simhost.AppArchiveLimit + 1); err != nil {
		t.Fatal(err)
	}
	sparse.Close()
	before := f.received.read.Load()
	if _, err := f.linux.daemon.forwardSimulator(ctx, "local-a", simhost.Request{Action: "install", App: app}); refusalOf(err) != "too_large" {
		t.Fatalf("oversized install = %v", err)
	}
	if read := f.received.read.Load() - before; read != 0 || f.commands.ran("xcrun simctl install") {
		t.Fatalf("an oversized app reached the Mac (%d bytes)", read)
	}
}

func TestARemoteSessionInstallsOnlyAnAppInItsFolders(t *testing.T) {
	f := simulatorsOnTwoHosts(t)
	ctx := context.Background()
	app := builtApp(t, 0)
	caller := &mcpCaller{session: surface.Session{ID: "local-a", Cwd: filepath.Dir(app)}, capabilities: []string{"simulator.drive"}}
	if _, err := f.linux.daemon.mcpSimulator(ctx, caller, mcpSimulatorInput{Action: "acquire", Kind: "iphone"}); err != nil {
		t.Fatal(err)
	}
	elsewhere := builtApp(t, 0)
	if _, err := f.linux.daemon.mcpSimulator(ctx, caller, mcpSimulatorInput{Action: "install", App: elsewhere}); refusalOf(err) != "out_of_scope" {
		t.Fatalf("install of an app outside the session's folders = %v", err)
	}
	if f.commands.ran("xcrun simctl install") {
		t.Fatal("an app outside the session's folders reached the Mac")
	}
	out, err := f.linux.daemon.mcpSimulator(ctx, caller, mcpSimulatorInput{Action: "install", App: app})
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(app)
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["installed"] != real || !f.commands.ran("xcrun simctl install U1 ") {
		t.Fatalf("install of the session's own app = %v", out)
	}
}
