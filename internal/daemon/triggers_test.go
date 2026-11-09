package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func triggerScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trigger.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func configureTriggers(t *testing.T, d *Daemon, body string) error {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(TriggersConfigPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(TriggersConfigPath(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return d.reloadTriggers()
}

func finishTriggerTurn(d *Daemon, fake *daemonSurface, session *surface.Session, turnID string) {
	for _, observation := range []*surface.TurnObservation{
		{Status: surface.StatusIdle},
		{Status: surface.StatusBusy, ActiveTurnID: turnID},
		{Status: surface.StatusIdle, CompletedTurnID: turnID, Reply: &surface.ReplyResult{Text: "done", Done: true}},
	} {
		fake.observations[session.ID] = observation
		d.observeSession(d.background.ctx, fake, session)
	}
}

func waitForTriggerRuns(t *testing.T, r *registry.Registry, trigger string, want int) []registry.TriggerRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		runs, err := r.ListTriggerRuns(registry.TriggerRunFilter{Trigger: trigger, Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		finished := 0
		for _, run := range runs {
			if run.State != registry.TriggerPending && run.State != registry.TriggerRunning {
				finished++
			}
		}
		if finished >= want {
			return runs
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d runs of %s finished: %+v", finished, want, trigger, runs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAFinishedTurnRunsItsTriggerOnceAndRecordsIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, _ := daemonFixture(t)
	out := filepath.Join(t.TempDir(), "completions")
	run := triggerScript(t, `cat >> "$OUT"; echo >> "$OUT"`)
	if err := configureTriggers(t, d, fmt.Sprintf(`{"version":1,"triggers":[{"name":"log","run":[%q],"env":{"OUT":%q}}]}`, run, out)); err != nil {
		t.Fatal(err)
	}
	finishTriggerTurn(d, fake, &from, "turn-1")
	d.observeSession(t.Context(), fake, &from)
	d.observeSession(t.Context(), fake, &from)
	runs := waitForTriggerRuns(t, r, "log", 1)
	if len(runs) != 1 || runs[0].State != registry.TriggerSucceeded || runs[0].SessionID != from.ID || runs[0].TurnID != "turn-1" {
		t.Fatalf("runs=%+v", runs)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var payload map[string]any
	if len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &payload) != nil {
		t.Fatalf("the trigger ran %d times: %q", len(lines), data)
	}
	if session, _ := payload["session"].(map[string]any); session["id"] != from.ID || payload["turnId"] != "turn-1" || payload["outcome"] != "finished" {
		t.Fatalf("payload=%v", payload)
	}
	history, err := r.ListHistory(20, from.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range history {
		found = found || entry.Kind == "trigger" && entry.Result == "log succeeded"
	}
	if !found {
		t.Fatalf("no trigger entry in history: %+v", history)
	}
}

func TestAFailedTurnFiresOnlyTriggersForFailures(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, to := daemonFixture(t)
	flag := filepath.Join(t.TempDir(), "failed")
	if err := configureTriggers(t, d, fmt.Sprintf(`{"version":1,"triggers":[{"name":"on-failure","touch":%q,"when":{"outcome":["failed"]}}]}`, flag)); err != nil {
		t.Fatal(err)
	}
	finishTriggerTurn(d, fake, &to, "turn-ok")
	for _, observation := range []*surface.TurnObservation{
		{Status: surface.StatusIdle},
		{Status: surface.StatusBusy, ActiveTurnID: "turn-bad"},
		{Status: surface.StatusIdle, CompletedTurnID: "turn-bad", Reply: &surface.ReplyResult{Error: "model overloaded", Done: true}},
	} {
		fake.observations[from.ID] = observation
		d.observeSession(t.Context(), fake, &from)
	}
	runs := waitForTriggerRuns(t, r, "on-failure", 1)
	if len(runs) != 1 || runs[0].TurnID != "turn-bad" || runs[0].State != registry.TriggerSucceeded {
		t.Fatalf("runs=%+v", runs)
	}
	if _, err := os.Stat(flag); err != nil {
		t.Fatal(err)
	}
}

func TestADelegatedSessionFiresOnlyTriggersThatOptIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, _ := daemonFixture(t)
	from.Source = "exec"
	fake.sessions[from.ID] = from
	if err := r.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := configureTriggers(t, d, fmt.Sprintf(`{"version":1,"triggers":[{"name":"all","touch":%q},{"name":"delegated","touch":%q,"delegated":true}]}`, filepath.Join(dir, "all"), filepath.Join(dir, "delegated"))); err != nil {
		t.Fatal(err)
	}
	finishTriggerTurn(d, fake, &from, "turn-1")
	waitForTriggerRuns(t, r, "delegated", 1)
	if runs, err := r.ListTriggerRuns(registry.TriggerRunFilter{Trigger: "all"}); err != nil || len(runs) != 0 {
		t.Fatalf("a delegated completion fired a trigger that did not opt in: %+v %v", runs, err)
	}
}

func TestTriggerRunsSurviveARestartWithoutRunningTwice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, _, _ := daemonFixture(t)
	out := filepath.Join(t.TempDir(), "runs")
	run := triggerScript(t, `echo "$AGENTHAIL_TURN_ID" >> "$OUT"`)
	config := fmt.Sprintf(`{"version":1,"triggers":[{"name":"log","run":[%q],"env":{"OUT":%q}}]}`, run, out)
	queued := []registry.TriggerRun{
		{Trigger: "log", DedupeKey: "h:s:turn-1", SessionID: "from", TurnID: "turn-1", Completion: json.RawMessage(`{"sessionId":"from","turnId":"turn-1"}`)},
		{Trigger: "log", DedupeKey: "h:s:turn-2", SessionID: "from", TurnID: "turn-2", Completion: json.RawMessage(`{"sessionId":"from","turnId":"turn-2"}`)},
	}
	if err := r.RecordCompletion("from", nil, queued); err != nil {
		t.Fatal(err)
	}
	if started, err := r.StartNextTriggerRun("log"); err != nil || started == nil || started.TurnID != "turn-1" {
		t.Fatalf("started=%+v err=%v", started, err)
	}
	if err := r.RecordCompletion("from", nil, queued); err != nil {
		t.Fatal(err)
	}
	d.background.stop()

	restarted := newTestDaemon(t, r, []surface.Surface{fake})
	if err := configureTriggers(t, restarted, config); err != nil {
		t.Fatal(err)
	}
	restarted.startTriggers()
	runs := waitForTriggerRuns(t, r, "log", 2)
	states := map[string]string{}
	for _, run := range runs {
		states[run.TurnID] = run.State
	}
	if len(runs) != 2 || states["turn-1"] != registry.TriggerInterrupted || states["turn-2"] != registry.TriggerSucceeded {
		t.Fatalf("runs=%+v", runs)
	}
	if data, _ := os.ReadFile(out); string(data) != "turn-2\n" {
		t.Fatalf("ran %q; an interrupted run must wait for a retry", data)
	}
}

func TestTriggersRunAtMostTwoAtOnceAndEachInOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, _, _, _ := daemonFixture(t)
	dir := t.TempDir()
	running, peaks := filepath.Join(dir, "running"), filepath.Join(dir, "peaks")
	if err := os.Mkdir(running, 0o700); err != nil {
		t.Fatal(err)
	}
	run := triggerScript(t, `mkdir "$RUNNING/$$"; ls "$RUNNING" | wc -l >> "$PEAKS"; echo "$AGENTHAIL_TURN_ID" >> "$LOG"; sleep 0.2; rmdir "$RUNNING/$$"`)
	var entries []string
	for _, name := range []string{"a", "b", "c"} {
		entries = append(entries, fmt.Sprintf(`{"name":%q,"run":[%q],"env":{"RUNNING":%q,"PEAKS":%q,"LOG":%q}}`, name, run, running, peaks, filepath.Join(dir, name)))
	}
	if err := configureTriggers(t, d, `{"version":1,"triggers":[`+strings.Join(entries, ",")+`]}`); err != nil {
		t.Fatal(err)
	}
	var queued []registry.TriggerRun
	for _, name := range []string{"a", "b", "c"} {
		for _, turn := range []string{"t1", "t2", "t3"} {
			queued = append(queued, registry.TriggerRun{Trigger: name, DedupeKey: "h:from:" + turn, SessionID: "from", TurnID: turn, Completion: json.RawMessage(`{"sessionId":"from","turnId":"` + turn + `"}`)})
		}
	}
	if err := r.RecordCompletion("from", nil, queued); err != nil {
		t.Fatal(err)
	}
	d.dispatchTriggers()
	for _, name := range []string{"a", "b", "c"} {
		waitForTriggerRuns(t, r, name, 3)
		if data, _ := os.ReadFile(filepath.Join(dir, name)); string(data) != "t1\nt2\nt3\n" {
			t.Fatalf("%s ran %q", name, data)
		}
	}
	data, err := os.ReadFile(peaks)
	if err != nil {
		t.Fatal(err)
	}
	for _, peak := range strings.Fields(string(data)) {
		if peak != "1" && peak != "2" {
			t.Fatalf("%s runs at once: %q", peak, data)
		}
	}
}

func TestAMalformedTriggerFileDisablesTriggersAndSaysWhy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, _ := daemonFixture(t)
	if err := configureTriggers(t, d, `{"version":1,"triggers":[{"name":"x","run":["make"]}]}`); err == nil {
		t.Fatal("a relative program loaded")
	}
	finishTriggerTurn(d, fake, &from, "turn-1")
	if runs, err := r.ListTriggerRuns(registry.TriggerRunFilter{}); err != nil || len(runs) != 0 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	api := signinAPIClient{t: t, handler: dashboardRouter(d), token: dashboardTestToken}
	var page triggersPage
	if code := api.do(http.MethodGet, "/api/v1/triggers", "", &page); code != http.StatusOK || page.Status.Loaded || !strings.Contains(page.Status.Error, "absolute") {
		t.Fatalf("code=%d page=%+v", code, page)
	}
	var refused signinAPIError
	if code := api.do(http.MethodPost, "/api/v1/triggers", `{"action":"reload"}`, &refused); code != http.StatusUnprocessableEntity || refused.Error.Code != "invalid_config" {
		t.Fatalf("code=%d error=%+v", code, refused)
	}
}

func TestOnlyTheLocalCLICanTestRetryOrReloadTriggers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, _, _, _ := daemonFixture(t)
	flag := filepath.Join(t.TempDir(), "flag")
	if err := configureTriggers(t, d, fmt.Sprintf(`{"version":1,"triggers":[{"name":"flag","touch":%q}]}`, flag)); err != nil {
		t.Fatal(err)
	}
	token := func(name, hostID string, scopes ...string) string {
		pairing, err := r.CreateDevicePairing(name, scopes, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		device, token, err := r.CompleteDevicePairing(pairing.Secret, name, hostID)
		if err != nil {
			t.Fatal(err)
		}
		if device.IsHost() != (hostID != "") {
			t.Fatalf("%s: host=%v", name, device.IsHost())
		}
		return token
	}
	handler := dashboardRouter(d)
	phone := signinAPIClient{t: t, handler: handler, token: token("Phone", "", "read", "control", "settings")}
	peer := signinAPIClient{t: t, handler: handler, token: token("vps", "h0123456789ab", "read", "control", registry.HostScope)}
	for name, remote := range map[string]signinAPIClient{"a device": phone, "a paired host": peer} {
		var page triggersPage
		if code := remote.do(http.MethodGet, "/api/v1/triggers", "", &page); code != http.StatusOK || len(page.Status.Triggers) != 1 {
			t.Fatalf("%s could not read triggers: code=%d page=%+v", name, code, page)
		}
	}
	retryable := registry.TriggerRun{Trigger: "flag", DedupeKey: "h:from:done", SessionID: "from", TurnID: "done", Completion: json.RawMessage(`{}`)}
	if err := r.RecordCompletion("from", nil, []registry.TriggerRun{retryable}); err != nil {
		t.Fatal(err)
	}
	finished, err := r.StartNextTriggerRun("flag")
	if err != nil || finished == nil {
		t.Fatalf("run=%+v err=%v", finished, err)
	}
	if err := r.FinishTriggerRun(finished.ID, registry.TriggerOutcome{State: registry.TriggerFailed, ExitCode: 1}, registry.HistoryEntry{Kind: "trigger", SessionID: "from", Result: "flag exited 1"}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"action":"reload"}`, `{"action":"test","trigger":"flag"}`, fmt.Sprintf(`{"action":"retry","runId":%d}`, finished.ID)} {
		for name, remote := range map[string]signinAPIClient{"a device token": phone, "a paired host's token": peer} {
			var refused signinAPIError
			if code := remote.do(http.MethodPost, "/api/v1/triggers", body, &refused); code != http.StatusForbidden || refused.Error.Code != "local_only" {
				t.Fatalf("%s posted %s: %d %+v", name, body, code, refused)
			}
		}
		if response := serveDashboardRequest(handler, http.MethodPost, "/api/v1/triggers", body); response.Code != http.StatusForbidden {
			t.Fatalf("a browser posted %s: %d", body, response.Code)
		}
	}
	if run, err := r.TriggerRun(finished.ID); err != nil || run.State != registry.TriggerFailed || run.Attempts != 1 {
		t.Fatalf("a refused retry changed the run: %+v %v", run, err)
	}
	if runs, _ := r.ListTriggerRuns(registry.TriggerRunFilter{}); len(runs) != 1 {
		t.Fatalf("a refused request queued %+v", runs)
	}

	cli := signinAPIClient{t: t, handler: handler, token: dashboardTestToken}
	var tested registry.TriggerRun
	if code := cli.do(http.MethodPost, "/api/v1/triggers", `{"action":"test","trigger":"flag"}`, &tested); code != http.StatusOK || !tested.Test {
		t.Fatalf("code=%d run=%+v", code, tested)
	}
	waitForTriggerRuns(t, r, "flag", 2)
	if err := os.Remove(flag); err != nil {
		t.Fatalf("the test run did not touch the file: %v", err)
	}
	var retried registry.TriggerRun
	if code := cli.do(http.MethodPost, "/api/v1/triggers", fmt.Sprintf(`{"action":"retry","runId":%d}`, tested.ID), &retried); code != http.StatusOK || retried.State != registry.TriggerPending {
		t.Fatalf("code=%d run=%+v", code, retried)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if run, err := r.TriggerRun(tested.ID); err == nil && run.State == registry.TriggerSucceeded && run.Attempts == 2 {
			break
		}
		if time.Now().After(deadline) {
			run, _ := r.TriggerRun(tested.ID)
			t.Fatalf("the retry did not run: %+v", run)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(flag); err != nil {
		t.Fatalf("the retry did not touch the file: %v", err)
	}
}

func TestARunQueuedAsItsTriggerFinishesDrainingStillRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, _, _, _ := daemonFixture(t)
	flag := filepath.Join(t.TempDir(), "flag")
	if err := configureTriggers(t, d, fmt.Sprintf(`{"version":1,"triggers":[{"name":"flag","touch":%q}]}`, flag)); err != nil {
		t.Fatal(err)
	}
	queue := func(turn string) {
		if err := r.RecordCompletion("from", nil, []registry.TriggerRun{{Trigger: "flag", DedupeKey: "h:from:" + turn, SessionID: "from", TurnID: turn}}); err != nil {
			t.Error(err)
		}
		d.dispatchTriggers()
	}
	var once sync.Once
	d.triggers.drained = func(string) { once.Do(func() { queue("t2") }) }
	queue("t1")
	runs := waitForTriggerRuns(t, r, "flag", 2)
	for _, run := range runs {
		if run.State != registry.TriggerSucceeded {
			t.Fatalf("runs=%+v", runs)
		}
	}
}

func TestAPairedHostsTurnFiresTriggersOnlyOnThatHost(t *testing.T) {
	viewer, host, record := pairedFederation(t)
	withoutDesktop(host.daemon)
	recordDesktop(viewer.daemon)
	for _, node := range []*federationNode{viewer, host} {
		node.daemon.triggers.path = filepath.Join(t.TempDir(), "triggers.json")
		body := fmt.Sprintf(`{"version":1,"triggers":[{"name":"any","touch":%q,"delegated":true}]}`, filepath.Join(t.TempDir(), "flag"))
		if err := os.WriteFile(node.daemon.triggers.path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := node.daemon.reloadTriggers(); err != nil {
			t.Fatal(err)
		}
	}
	hostTurn(t, host, "remote-a", "turn-1", "", nil)
	awaitMirroredCompletion(t, viewer, "remote-a", "turn-1")
	settleNotices(t, host, record.ID, viewer)
	if runs := waitForTriggerRuns(t, host.reg, "any", 1); len(runs) != 1 || runs[0].TurnID != "turn-1" {
		t.Fatalf("host runs=%+v", runs)
	}
	if runs, err := viewer.reg.ListTriggerRuns(registry.TriggerRunFilter{}); err != nil || len(runs) != 0 {
		t.Fatalf("the viewer fired its own trigger for a mirrored turn: %+v %v", runs, err)
	}
}
