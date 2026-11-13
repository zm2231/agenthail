package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/nodehost"
	"github.com/zm2231/agenthail/internal/poll"
	"github.com/zm2231/agenthail/internal/testnode"
)

const fixtureServer = `
import fs from 'node:fs';
import path from 'node:path';

type Event = { type: string; entityId?: string };

export default async function (ah: any) {
  const hold = path.join(ah.dir, 'hold-load');
  while (fs.existsSync(hold)) await new Promise(resolve => setTimeout(resolve, 20));
  const interval = path.join(ah.dir, 'slow-interval');
  const slowEvery = fs.existsSync(interval) ? Number(fs.readFileSync(interval, 'utf8')) : 60;
  const events = ah.table('events');
  const ticks = ah.table('ticks');
  ah.db.exec("CREATE TABLE IF NOT EXISTS " + events + " (type TEXT, entity TEXT)");
  ah.db.exec("CREATE TABLE IF NOT EXISTS " + ticks + " (at TEXT)");
  const slow = ah.table('slow');
  ah.db.exec("CREATE TABLE IF NOT EXISTS " + slow + " (phase TEXT)");
  ah.job({ name: 'slow', everySeconds: slowEvery, run: async () => {
    ah.db.prepare("INSERT INTO " + slow + " VALUES ('start')").run();
    await new Promise(resolve => setTimeout(resolve, 2500));
    ah.db.prepare("INSERT INTO " + slow + " VALUES ('end')").run();
  } });
  ah.on('turn.completed', (event: Event) => {
    ah.db.prepare("INSERT INTO " + events + " VALUES (?, ?)").run(event.type, event.entityId ?? '');
  });
  ah.job({ name: 'tick', everySeconds: 60, run: () => {
    ah.db.prepare("INSERT INTO " + ticks + " VALUES (?)").run(new Date().toISOString());
  } });
  ah.action('state', () => ({
    events: ah.db.prepare("SELECT entity FROM " + events + " ORDER BY rowid").all().map((row: any) => row.entity),
    ticks: ah.db.prepare("SELECT count(*) AS n FROM " + ticks).get().n,
    slowStarts: ah.db.prepare("SELECT count(*) AS n FROM " + slow + " WHERE phase = 'start'").get().n,
    slowEnds: ah.db.prepare("SELECT count(*) AS n FROM " + slow + " WHERE phase = 'end'").get().n,
  }));
  ah.action('sessions', async () => (await ah.call('agenthail_sessions', {})).sessions.map((s: any) => s.id));
  ah.action('send', async (input: { message: string }, ctx: any) => {
    const sent = await ah.call('agenthail_send', { target: 'to', message: input.message });
    return { status: sent.status, caller: ctx.caller.kind };
  });
  ah.action('create', () => ah.call('agenthail_create', { surface: 'claude', message: 'hi' }));
  ah.action('caller', (_: unknown, ctx: any) => ctx.caller);
  ah.action('held', async () => {
    fs.writeFileSync(path.join(ah.dir, 'held-started'), '');
    while (!fs.existsSync(path.join(ah.dir, 'held-release'))) await new Promise(resolve => setTimeout(resolve, 20));
    return 'released';
  });
}
`

type extensionFixture struct {
	d       *Daemon
	fake    *daemonSurface
	server  *httptest.Server
	home    string
	stop    func()
	workdir string
}

func startExtensionFixture(t *testing.T) *extensionFixture {
	t.Helper()
	return startExtensionFixtureWith(t, nil)
}

func startExtensionFixtureWith(t *testing.T, replace map[string]string) *extensionFixture {
	t.Helper()
	home := extensionTestHome(t)
	dir := filepath.Join(extension.Workspace(home), "fixture")
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"extension.json":  `{"label": "Fixture", "capabilities": ["read.all", "control.all"]}`,
		"server.ts":       fixtureServer,
		"ui.html":         `<!doctype html><script type="module">import { action } from '/x/_ah.js';</script>`,
		"assets/app.css":  `body { color: red }`,
		"notes-secret.md": `not served`,
	}
	maps.Copy(files, replace)
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f := startExtensionDaemon(t, home, extensionDaemonOptions{})
	f.workdir = dir
	f.awaitState(t, "loaded")
	return f
}

func extensionTestHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "ext")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	return home
}

type extensionDaemonOptions struct {
	log    io.Writer
	launch func(nodehost.Launch) (nodehost.Launch, error)
}

func startExtensionDaemon(t *testing.T, home string, opts extensionDaemonOptions) *extensionFixture {
	t.Helper()
	d, _, fake, _, handler, _ := baseFixture(t)
	if opts.log != nil {
		d.log = log.New(opts.log, "", 0)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	script := filepath.Join(testnode.ExtensionHost(t), "host.mjs")
	launch := nodehost.Launch{Node: testnode.Path(t), Script: script, Version: "test", Env: os.Environ()}
	d.SetExtensionHost(home, func() (nodehost.Launch, error) {
		if opts.launch != nil {
			return opts.launch(launch)
		}
		return launch, nil
	})
	d.extensions.host.MinRestart = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.superviseExtensions(ctx)
	}()
	f := &extensionFixture{d: d, fake: fake, server: server, home: home, stop: func() { cancel(); <-done }}
	t.Cleanup(f.stop)
	return f
}

func (f *extensionFixture) request(t *testing.T, method, path, token string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequest(method, f.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(response.Body)
	return response, out.Bytes()
}

func (f *extensionFixture) action(t *testing.T, name string, input any) (int, map[string]json.RawMessage) {
	t.Helper()
	response, body := f.request(t, http.MethodPost, "/x/fixture/a/"+name, "secret", input)
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("action %s answered %d %s", name, response.StatusCode, body)
	}
	return response.StatusCode, decoded
}

func (f *extensionFixture) awaitState(t *testing.T, state string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var last extensionsView
	if err := poll.Until(ctx, 20*time.Millisecond, func() (bool, error) {
		last = f.d.extensionsView()
		return last.Host == "running" && len(last.Extensions) == 1 && last.Extensions[0].State == state, nil
	}); err != nil {
		t.Fatalf("the fixture extension never reached %s: %+v", state, last)
	}
}

type fixtureState struct {
	Events     []string `json:"events"`
	Ticks      int      `json:"ticks"`
	SlowStarts int      `json:"slowStarts"`
	SlowEnds   int      `json:"slowEnds"`
}

func (f *extensionFixture) state(t *testing.T) fixtureState {
	t.Helper()
	status, body := f.action(t, "state", nil)
	var state fixtureState
	if status != http.StatusOK || json.Unmarshal(body["result"], &state) != nil {
		t.Fatalf("state answered %d %s", status, body["result"])
	}
	return state
}

func extensionRefusalCode(body map[string]json.RawMessage) string {
	var refusal struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body["error"], &refusal)
	return refusal.Code
}

func TestAnExtensionServesItsPageOnlyToAuthorizedClientsAndNeverItsServerFile(t *testing.T) {
	f := startExtensionFixture(t)
	for _, path := range []string{"/x/fixture/", "/x/fixture", "/x/_ah.js", "/x/fixture/assets/app.css"} {
		if response, _ := f.request(t, http.MethodGet, path, "", nil); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s without credentials answered %d", path, response.StatusCode)
		}
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	bare, _ := http.NewRequest(http.MethodGet, f.server.URL+"/x/fixture", nil)
	bare.Header.Set("Authorization", "Bearer secret")
	if response, err := noRedirect.Do(bare); err != nil || response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/x/fixture/" {
		t.Fatalf("authorized /x/fixture: %v %+v", err, response)
	}
	if response, _ := f.request(t, http.MethodPost, "/x/fixture/a/state", "", nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("action without credentials answered %d", response.StatusCode)
	}
	response, body := f.request(t, http.MethodGet, "/x/fixture/", "secret", nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "/x/_ah.js") {
		t.Fatalf("page answered %d %s", response.StatusCode, body)
	}
	if csp := response.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") || response.Header.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("page frame headers: %v", response.Header)
	}
	if response, _ := f.request(t, http.MethodGet, "/x/fixture/assets/app.css", "secret", nil); response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("asset answered %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	for _, path := range []string{"/x/fixture/server.ts", "/x/fixture/extension.json", "/x/fixture/notes-secret.md", "/x/fixture/assets/../server.ts"} {
		if response, _ := f.request(t, http.MethodGet, path, "secret", nil); response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s answered %d", path, response.StatusCode)
		}
	}
	if response, _ := f.request(t, http.MethodGet, "/", "", nil); response.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("the dashboard lost its frame denial: %v", response.Header)
	}
}

func TestAnExtensionActsOnlyWithTheCapabilitiesThePersonApproved(t *testing.T) {
	f := startExtensionFixture(t)
	if view := f.d.extensionsView(); len(view.Extensions[0].Pending) != 2 || len(view.Extensions[0].Capabilities) != 0 {
		t.Fatalf("before approval: %+v", view.Extensions[0])
	}
	status, body := f.action(t, "send", map[string]string{"message": "before approval"})
	if status == http.StatusOK || extensionRefusalCode(body) != "out_of_scope" {
		t.Fatalf("an unapproved send answered %d %s", status, body)
	}
	status, body = f.action(t, "create", nil)
	if status == http.StatusOK || extensionRefusalCode(body) != "not_for_extensions" {
		t.Fatalf("an agent-only tool answered %d %s", status, body)
	}

	if response, body := f.request(t, http.MethodPost, "/api/v1/extensions", "secret", map[string]string{"action": "approve", "id": "fixture"}); response.StatusCode != http.StatusOK {
		t.Fatalf("approve answered %d %s", response.StatusCode, body)
	}
	if view := f.d.extensionsView(); len(view.Extensions[0].Capabilities) != 2 || len(view.Extensions[0].Pending) != 0 {
		t.Fatalf("after approval: %+v", view.Extensions[0])
	}
	status, body = f.action(t, "sessions", nil)
	if status != http.StatusOK || !strings.Contains(string(body["result"]), `"to"`) {
		t.Fatalf("sessions after approval answered %d %s", status, body)
	}
	status, body = f.action(t, "send", map[string]string{"message": "from the extension"})
	if status != http.StatusOK || !strings.Contains(string(body["result"]), `"caller":"person"`) {
		t.Fatalf("send after approval answered %d %s", status, body)
	}
	history, err := f.d.Registry.ListHistory(50, "to")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range history {
		if entry.SourceSessionID == "extension:fixture" {
			found = true
		}
		if entry.SourceSessionID == "" && strings.Contains(entry.Message, "from the extension") {
			t.Fatalf("the extension's send was recorded without a sender, as the person's would be: %+v", entry)
		}
	}
	if !found {
		t.Fatalf("no history entry names the extension as sender: %+v", history)
	}
}

func TestAnExtensionGetsEventsAndJobTicksAndKeepsEventsPublishedWhileItsHostRestarts(t *testing.T) {
	f := startExtensionFixture(t)
	if err := f.d.extensions.approvals.Grant("fixture", extension.Manifest{Capabilities: []string{"read.all", "control.all"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.reloadExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.d.publishEvent("turn.completed", "first", map[string]string{})
	eventually(t, "the handler records the event and the job ticks", func() bool {
		state := f.state(t)
		return len(state.Events) == 1 && state.Events[0] == "first" && state.Ticks >= 1
	})

	hello, err := f.d.extensions.host.Client().Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(hello.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the daemon sees the host go down", func() bool { return f.d.extensionsView().Host != "running" })
	f.d.publishEvent("turn.completed", "while-down", map[string]string{})
	f.awaitState(t, "loaded")
	eventually(t, "the restarted host gets the event published while it was down", func() bool {
		state := f.state(t)
		return len(state.Events) == 2 && state.Events[1] == "while-down"
	})
}

func TestAnExtensionThatFailsToLoadIsReportedAndOthersKeepWorking(t *testing.T) {
	f := startExtensionFixture(t)
	broken := filepath.Join(extension.Workspace(f.home), "broken")
	if err := os.MkdirAll(broken, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "extension.json"), []byte(`{"label": "Broken"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "server.ts"), []byte(`export default function () { throw new Error('no data source configured'); }`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.d.reloadExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	view := f.d.extensionsView()
	if len(view.Extensions) != 2 || view.Extensions[0].ID != "broken" || view.Extensions[0].State != "failed" || !strings.Contains(view.Extensions[0].Error, "no data source configured") {
		t.Fatalf("view: %+v", view.Extensions)
	}
	if status, _ := f.action(t, "state", nil); status != http.StatusOK {
		t.Fatalf("the healthy extension answered %d", status)
	}
	if response, body := f.request(t, http.MethodPost, "/x/broken/a/anything", "secret", nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("the broken extension answered %d %s", response.StatusCode, body)
	}
}

func TestReloadsRunOneAtATimeAndNeverStartAJobThatIsStillRunning(t *testing.T) {
	f := startExtensionFixture(t)
	if err := f.d.extensions.approvals.Grant("fixture", extension.Manifest{Capabilities: []string{"read.all", "control.all"}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the slow job starts", func() bool { return f.state(t).SlowStarts == 1 })

	if err := os.WriteFile(filepath.Join(f.workdir, "slow-interval"), []byte("120"), 0o600); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 4)
	for range 4 {
		go func() { errs <- f.d.reloadExtensions(context.Background()) }()
	}
	for range 4 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if status, body := f.action(t, "sessions", nil); status != http.StatusOK {
		t.Fatalf("after concurrent reloads the extension's own token answered %d %s", status, body)
	}
	eventually(t, "the slow job's first run ends", func() bool { return f.state(t).SlowEnds == 1 })
	time.Sleep(3 * extensionJobTick)
	if state := f.state(t); state.SlowStarts != 1 {
		t.Fatalf("the slow job started %d times; a reload must not start a run while one is going", state.SlowStarts)
	}
	f.d.extensions.mu.Lock()
	job := f.d.extensions.loaded["fixture"].jobs["slow"]
	running, lastRun, due := job.running, job.LastRun, job.due
	f.d.extensions.mu.Unlock()
	if running || lastRun.IsZero() || !due.Equal(lastRun.Add(2*time.Minute)) {
		t.Fatalf("after the run: running=%v lastRun=%v due=%v", running, lastRun, due)
	}
}

func TestEventsPublishedWhileExtensionsReloadAreDeliveredOnceTheyLoad(t *testing.T) {
	f := startExtensionFixture(t)
	if err := f.d.extensions.approvals.Grant("fixture", extension.Manifest{Capabilities: []string{"read.all", "control.all"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.reloadExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	hold := filepath.Join(f.workdir, "hold-load")
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded := make(chan error, 1)
	go func() { reloaded <- f.d.reloadExtensions(context.Background()) }()
	eventually(t, "the reload is under way", func() bool {
		f.d.extensions.mu.Lock()
		defer f.d.extensions.mu.Unlock()
		return f.d.extensions.reloading
	})
	f.d.publishEvent("turn.completed", "during-reload", map[string]string{})
	time.Sleep(300 * time.Millisecond)
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	if err := <-reloaded; err != nil {
		t.Fatal(err)
	}
	eventually(t, "the event published during the reload arrives", func() bool {
		state := f.state(t)
		return len(state.Events) == 1 && state.Events[0] == "during-reload"
	})
}

func TestAReloadTheHostCannotFinishRestartsItAndHeldEventsStillArrive(t *testing.T) {
	f := startExtensionFixture(t)
	if err := f.d.extensions.approvals.Grant("fixture", extension.Manifest{Capabilities: []string{"read.all", "control.all"}}); err != nil {
		t.Fatal(err)
	}
	if err := f.d.reloadExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := f.d.extensions.host.Client().Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.workdir, "hold-load"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := f.d.reloadExtensions(ctx); !errors.Is(err, errExtensionLoadFailed) {
		t.Fatalf("a reload the host could not finish answered %v", err)
	}
	f.d.publishEvent("turn.completed", "after-failed-load", map[string]string{})
	if err := os.Remove(filepath.Join(f.workdir, "hold-load")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a new host loads and gets the held event", func() bool {
		hello, err := f.d.extensions.host.Client().Hello(context.Background())
		if err != nil || hello.PID == before.PID || f.d.extensionsView().Extensions[0].State != "loaded" {
			return false
		}
		state := f.state(t)
		return len(state.Events) == 1 && state.Events[0] == "after-failed-load"
	})
}

func TestTheExtensionHostRunsWithNoExtensionsAndLoadsTheFirstOnOneReload(t *testing.T) {
	f := startExtensionDaemon(t, extensionTestHome(t), extensionDaemonOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var view extensionsView
	if err := poll.Until(ctx, 20*time.Millisecond, func() (bool, error) {
		view = f.d.extensionsView()
		return view.Host == "running", nil
	}); err != nil {
		t.Fatalf("the extension host never ran with no extensions: %+v", view)
	}

	writeExtension(t, extension.Workspace(f.home), "notes", `{"label": "Notes"}`)
	response, body := f.request(t, http.MethodPost, "/api/v1/extensions", "secret", map[string]string{"action": "reload"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the first reload after adding an extension = %d %s", response.StatusCode, body)
	}
	view = f.d.extensionsView()
	if view.Host != "running" || len(view.Extensions) != 1 || view.Extensions[0].ID != "notes" || view.Extensions[0].State != "loaded" {
		t.Fatalf("after one reload: %+v", view)
	}
}

func TestAnExtensionHostThatKeepsCrashingIsLoggedOnceAndAgainWhenItRecovers(t *testing.T) {
	home := extensionTestHome(t)
	crash := filepath.Join(home, "crash.mjs")
	if err := os.WriteFile(crash, []byte("console.error('crashed at ' + Date.now()); process.exit(3);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logged syncLog
	var crashing atomic.Bool
	var launches atomic.Int32
	crashing.Store(true)
	f := startExtensionDaemon(t, home, extensionDaemonOptions{log: &logged, launch: func(real nodehost.Launch) (nodehost.Launch, error) {
		launches.Add(1)
		if crashing.Load() {
			real.Script = crash
		}
		return real, nil
	}})
	eventually(t, "the host retries on backoff", func() bool { return launches.Load() >= 4 })
	failures := func() []string {
		var lines []string
		for line := range strings.Lines(logged.String()) {
			if strings.HasPrefix(line, "extension host: ") && !strings.Contains(line, "loaded again") {
				lines = append(lines, line)
			}
		}
		return lines
	}
	if lines := failures(); len(lines) != 1 || !strings.Contains(lines[0], "crashed at") {
		t.Fatalf("a crash repeated %d times was logged as %q", launches.Load(), lines)
	}

	crashing.Store(false)
	eventually(t, "the host runs", func() bool { return f.d.extensionsView().Host == "running" })
	if n := strings.Count(logged.String(), "extension host: loaded again"); n != 1 {
		t.Fatalf("recovery logged %d times:\n%s", n, logged.String())
	}
	if lines := failures(); len(lines) != 1 {
		t.Fatalf("failures logged after recovery: %q", lines)
	}
}
