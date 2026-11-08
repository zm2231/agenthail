package surfaces

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

const otherManagedThreadID = "019f004a-0000-7000-8000-00000000cafe"

func fastTerminalPolls(t *testing.T) {
	t.Helper()
	active, idle, tick, retry, settle := codexTerminalsActivePoll, codexTerminalsIdlePoll, codexTerminalsTick, codexWatchRetry, codexTerminalSettle
	codexTerminalsActivePoll, codexTerminalsIdlePoll, codexTerminalsTick, codexWatchRetry, codexTerminalSettle = 40*time.Millisecond, 200*time.Millisecond, 10*time.Millisecond, 10*time.Second, 300*time.Millisecond
	t.Cleanup(func() {
		codexTerminalsActivePoll, codexTerminalsIdlePoll, codexTerminalsTick, codexWatchRetry, codexTerminalSettle = active, idle, tick, retry, settle
	})
}

func watchedCodex(t *testing.T, threadID string) *Codex {
	t.Helper()
	return watchedCodexWaking(t, threadID, func() {})
}

func watchedCodexWaking(t *testing.T, threadID string, changed func()) *Codex {
	t.Helper()
	codex := NewCodex("1")
	codex.noteLoaded(&codexRuntimes{managed: &managedCodexClient{}, managedLoaded: map[string]bool{threadID: true}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		codex.NotifyCatalog(ctx, changed)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return codex
}

func listedTerminals(codex *Codex, threadID string) *surface.BackgroundTasks {
	return codex.codexBackground(context.Background(), threadID, "", nil)
}

func terminalsReply(running *atomic.Bool) codextest.Responder {
	return func(method string, _ map[string]any) map[string]any {
		if method != codexTerminalsMethod {
			return nil
		}
		if !running.Load() {
			return map[string]any{"data": []any{}}
		}
		return map[string]any{"data": []any{map[string]any{"processId": "7", "itemId": "call-dev", "command": "npm run dev", "cwd": "/"}}}
	}
}

func TestACodexAppServerWithoutTheTerminalListingShowsTheTranscriptsBackgroundAndStopsAsking(t *testing.T) {
	fastTerminalPolls(t)
	daemon := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == codexTerminalsMethod {
			return map[string]any{"error": map[string]any{"code": -32600, "message": "thread/backgroundTerminals/list requires experimentalApi capability"}}
		}
		return nil
	})
	daemon.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Cwd: t.TempDir()})
	daemon.Load(managedThreadID)
	daemon.AddThread(codextest.Thread{ID: otherManagedThreadID})
	daemon.Load(otherManagedThreadID)
	appendLines(t, daemon.RolloutPath(managedThreadID), codexBackgroundRollout()[:4])
	codex := watchedCodex(t, managedThreadID)
	testkit.Eventually(t, "the watch to ask for the thread's terminals", 5*time.Second, func() bool {
		return len(daemon.Calls(codexTerminalsMethod)) > 0
	})
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "active"}})
	time.Sleep(300 * time.Millisecond)
	if calls := len(daemon.Calls(codexTerminalsMethod)); calls != 1 {
		t.Fatalf("the watch asked %d times after the app-server refused the listing", calls)
	}
	background := listedCodexSession(t, codex, managedThreadID).BackgroundTasks
	if !surface.BackgroundRunning(background) || background.Source != surface.BackgroundSourceTranscript || len(background.Tasks) != 1 || background.Tasks[0].Description != "make build" {
		t.Fatalf("background=%+v, want the transcript's running command", background)
	}
}

func TestAnIdleCodexThreadIsPolledForTerminalsOnlyWhileItHasSome(t *testing.T) {
	fastTerminalPolls(t)
	var running atomic.Bool
	daemon := startManagedCodex(t, terminalsReply(&running))
	daemon.AddThread(codextest.Thread{ID: managedThreadID})
	daemon.Load(managedThreadID)
	codex := watchedCodex(t, managedThreadID)
	calls := func() int { return len(daemon.Calls(codexTerminalsMethod)) }
	testkit.Eventually(t, "the watch to list the loaded thread again once a command its last turn started would show", 5*time.Second, func() bool { return calls() == 2 })
	time.Sleep(500 * time.Millisecond)
	if got := calls(); got != 2 {
		t.Fatalf("an idle thread with no terminals was listed %d times", got)
	}

	running.Store(true)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "active"}})
	testkit.Eventually(t, "the row to list the dev server", 5*time.Second, func() bool {
		background := listedTerminals(codex, managedThreadID)
		return surface.BackgroundRunning(background) && background.Source == surface.BackgroundSourceRuntime &&
			len(background.Tasks) == 1 && background.Tasks[0].ID == "7" && background.Tasks[0].Description == "npm run dev"
	})
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "idle"}})
	time.Sleep(100 * time.Millisecond)
	before := calls()
	time.Sleep(time.Second)
	if polls := calls() - before; polls < 1 || polls > 8 {
		t.Fatalf("an idle thread with a terminal was listed %d times in a second, every %s expected", polls, codexTerminalsIdlePoll)
	}

	running.Store(false)
	testkit.Eventually(t, "the row to drop the exited terminal", 5*time.Second, func() bool {
		return listedTerminals(codex, managedThreadID) == nil
	})
	settled := calls()
	time.Sleep(500 * time.Millisecond)
	if got := calls(); got != settled {
		t.Fatalf("an idle thread whose terminals exited was listed %d more times", got-settled)
	}
}

func TestACodexThreadsTerminalsClearOnceNothingObservesThem(t *testing.T) {
	fastTerminalPolls(t)
	var running, drop atomic.Bool
	running.Store(true)
	reply := terminalsReply(&running)
	daemon := startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		if method == codexTerminalsMethod && drop.Load() {
			return codextest.Drop
		}
		return reply(method, params)
	})
	daemon.AddThread(codextest.Thread{ID: managedThreadID})
	daemon.Load(managedThreadID)
	codex := watchedCodex(t, managedThreadID)
	listed := func() bool { return surface.BackgroundRunning(listedTerminals(codex, managedThreadID)) }
	testkit.Eventually(t, "the row to list the terminal", 5*time.Second, listed)

	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "notLoaded"}})
	testkit.Eventually(t, "an unloaded thread to drop its terminals", 5*time.Second, func() bool { return !listed() })

	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "active"}})
	testkit.Eventually(t, "the reloaded thread to list its terminal", 5*time.Second, listed)
	drop.Store(true)
	testkit.Eventually(t, "the terminal to clear when the watch loses its connection", 5*time.Second, func() bool { return !listed() })
}

func TestACodexThreadHasOneTerminalListingInFlightAtATime(t *testing.T) {
	fastTerminalPolls(t)
	var running, slow atomic.Bool
	running.Store(true)
	slow.Store(true)
	reply := terminalsReply(&running)
	daemon := startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		if method == codexTerminalsMethod && slow.Load() {
			time.Sleep(200 * time.Millisecond)
		}
		return reply(method, params)
	})
	daemon.AddThread(codextest.Thread{ID: managedThreadID})
	daemon.Load(managedThreadID)
	codex := watchedCodex(t, managedThreadID)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "active"}})
	time.Sleep(time.Second)
	running.Store(false)
	slow.Store(false)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "idle"}})
	testkit.Eventually(t, "the row to drop the terminal", 5*time.Second, func() bool { return listedTerminals(codex, managedThreadID) == nil })
	time.Sleep(300 * time.Millisecond)
	if calls := len(daemon.Calls(codexTerminalsMethod)); calls > 10 {
		t.Fatalf("a second of 200ms listings sent %d requests; each must wait for the last", calls)
	}
}

func TestACommandTheTranscriptJustStartedRunsUntilAListingAfterItLeavesItOut(t *testing.T) {
	fastTerminalPolls(t)
	var running atomic.Bool
	daemon := startManagedCodex(t, terminalsReply(&running))
	daemon.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Cwd: t.TempDir()})
	daemon.Load(managedThreadID)
	path := daemon.RolloutPath(managedThreadID)
	codex := watchedCodex(t, managedThreadID)
	background := func() *surface.BackgroundTasks {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return codex.codexBackground(context.Background(), managedThreadID, path, info)
	}
	testkit.Eventually(t, "the thread's first listing", 5*time.Second, func() bool { return len(daemon.Calls(codexTerminalsMethod)) > 0 })
	time.Sleep(50 * time.Millisecond)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	appendLines(t, path, []string{
		`{"timestamp":"` + now + `","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"` + now + `","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call-dev","arguments":"{\"cmd\":\"npm run dev\",\"yield_time_ms\":250}"}}`,
		`{"timestamp":"` + now + `","type":"response_item","payload":{"type":"function_call_output","call_id":"call-dev","output":"Process running with session ID 7\nOutput:\n"}}`,
		`{"timestamp":"` + now + `","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
	})
	if got := background(); !surface.BackgroundRunning(got) || len(got.Tasks) != 1 || got.Tasks[0].Description != "npm run dev" {
		t.Fatalf("background=%+v right after the turn started the command", got)
	}
	running.Store(true)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "idle"}})
	testkit.Eventually(t, "the listing to take over the command", 5*time.Second, func() bool {
		got := background()
		return surface.BackgroundRunning(got) && got.Source == surface.BackgroundSourceRuntime && len(got.Tasks) == 1 && got.Tasks[0].ID == "7"
	})
	running.Store(false)
	testkit.Eventually(t, "a later listing without the command to end it", 5*time.Second, func() bool { return background() == nil })
}

func TestACommandThatEndsBeforeAnyListingSeesItLeavesTheRowWithoutWaitingForDiscovery(t *testing.T) {
	fastTerminalPolls(t)
	var running atomic.Bool
	daemon := startManagedCodex(t, terminalsReply(&running))
	daemon.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Cwd: t.TempDir()})
	daemon.Load(managedThreadID)
	path := daemon.RolloutPath(managedThreadID)
	var wakes atomic.Int64
	codex := watchedCodexWaking(t, managedThreadID, func() { wakes.Add(1) })
	testkit.Eventually(t, "the idle thread's listings to settle", 5*time.Second, func() bool { return len(daemon.Calls(codexTerminalsMethod)) == 2 })

	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "active"}})
	now := time.Now().UTC().Format(time.RFC3339Nano)
	appendLines(t, path, []string{
		`{"timestamp":"` + now + `","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"` + now + `","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call-quick","arguments":"{\"cmd\":\"sleep 0.1\",\"yield_time_ms\":50}"}}`,
		`{"timestamp":"` + now + `","type":"response_item","payload":{"type":"function_call_output","call_id":"call-quick","output":"Process running with session ID 9\nOutput:\n"}}`,
		`{"timestamp":"` + now + `","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
	})
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "idle"}})
	background := func() *surface.BackgroundTasks {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return codex.codexBackground(context.Background(), managedThreadID, path, info)
	}
	if got := background(); !surface.BackgroundRunning(got) {
		t.Fatalf("background=%+v right after the turn started the command", got)
	}
	time.Sleep(50 * time.Millisecond)
	before := wakes.Load()
	testkit.Eventually(t, "a wake once a listing past the settle window leaves the command out", 5*time.Second, func() bool {
		return wakes.Load() > before && background() == nil
	})
}

func TestAThreadItsAppServerNoLongerHasDoesNotFallBackToItsTranscript(t *testing.T) {
	fastTerminalPolls(t)
	var gone atomic.Bool
	daemon := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method != codexTerminalsMethod {
			return nil
		}
		if gone.Load() {
			return map[string]any{"error": map[string]any{"code": -32600, "message": "thread not found: " + managedThreadID}}
		}
		return map[string]any{"data": []any{}}
	})
	daemon.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Cwd: t.TempDir()})
	daemon.Load(managedThreadID)
	path := daemon.RolloutPath(managedThreadID)
	appendLines(t, path, codexBackgroundRollout()[:4])
	codex := watchedCodex(t, managedThreadID)
	background := func() *surface.BackgroundTasks {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return codex.codexBackground(context.Background(), managedThreadID, path, info)
	}
	testkit.Eventually(t, "a listing to rule out the transcript's old command", 5*time.Second, func() bool {
		return len(daemon.Calls(codexTerminalsMethod)) == 2 && background() == nil
	})

	gone.Store(true)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "active"}})
	testkit.Eventually(t, "the watch to hear the thread is gone", 5*time.Second, func() bool { return len(daemon.Calls(codexTerminalsMethod)) == 3 })
	time.Sleep(100 * time.Millisecond)
	if got := background(); got != nil {
		t.Fatalf("background=%+v after the app-server said the thread is not loaded", got)
	}
}

func TestAListingAskedBeforeAThreadReloadedDoesNotEraseItsNewTerminals(t *testing.T) {
	fastTerminalPolls(t)
	var calls, blockAt atomic.Int64
	var running atomic.Bool
	release := make(chan struct{})
	daemon := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method != codexTerminalsMethod {
			return nil
		}
		if calls.Add(1) == blockAt.Load() {
			<-release
			return map[string]any{"error": map[string]any{"code": -32600, "message": "thread not found: " + managedThreadID}}
		}
		if !running.Load() {
			return map[string]any{"data": []any{}}
		}
		return map[string]any{"data": []any{map[string]any{"processId": "11", "itemId": "call-new", "command": "npm test -- --watch", "cwd": "/"}}}
	})
	daemon.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Cwd: t.TempDir()})
	daemon.Load(managedThreadID)
	path := daemon.RolloutPath(managedThreadID)
	codex := watchedCodex(t, managedThreadID)
	testkit.Eventually(t, "the idle thread's listings to settle", 5*time.Second, func() bool { return calls.Load() == 2 })

	blockAt.Store(3)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "active"}})
	testkit.Eventually(t, "a listing to be held open", 5*time.Second, func() bool { return calls.Load() == 3 })
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "notLoaded"}})
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "active"}})
	now := time.Now().UTC().Format(time.RFC3339Nano)
	appendLines(t, path, []string{
		`{"timestamp":"` + now + `","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}`,
		`{"timestamp":"` + now + `","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call-new","arguments":"{\"cmd\":\"npm test -- --watch\",\"yield_time_ms\":250}"}}`,
		`{"timestamp":"` + now + `","type":"response_item","payload":{"type":"function_call_output","call_id":"call-new","output":"Process running with session ID 11\nOutput:\n"}}`,
	})
	running.Store(true)
	time.Sleep(50 * time.Millisecond)
	close(release)

	testkit.Eventually(t, "the reloaded thread to list its new terminal", 5*time.Second, func() bool {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		background := codex.codexBackground(context.Background(), managedThreadID, path, info)
		return surface.BackgroundRunning(background) && background.Source == surface.BackgroundSourceRuntime && len(background.Tasks) == 1 && background.Tasks[0].ID == "11"
	})
}
