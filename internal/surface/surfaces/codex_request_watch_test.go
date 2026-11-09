package surfaces

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func runningThread(id string) codextest.Thread {
	return codextest.Thread{ID: id, Originator: "agenthail", Turns: []codextest.Turn{{ID: "turn-1", Status: "inProgress", User: "Clean the build"}}}
}

func approvalOn(threadID, itemID string) map[string]any {
	return map[string]any{"threadId": threadID, "turnId": "turn-1", "itemId": itemID, "command": "rm -rf " + itemID, "cwd": "/work"}
}

func waitingStatus(flags ...any) map[string]any {
	return map[string]any{"type": "active", "activeFlags": append([]any{}, flags...)}
}

func resumesOf(daemon *codextest.Daemon, threadID string) int {
	count := 0
	for _, call := range daemon.Calls(codexThreadResume) {
		if call["threadId"] == threadID {
			count++
		}
	}
	return count
}

func TestTheStatusWatchHearsASecondCodexRequestAndOneOfSeveralResolving(t *testing.T) {
	fastTerminalPolls(t)
	daemon := startManagedCodex(t, nil)
	daemon.AddThread(runningThread(managedThreadID))
	daemon.Load(managedThreadID)
	var changes atomic.Int32
	watchedCodexWaking(t, managedThreadID, func() { changes.Add(1) })
	testkit.Eventually(t, "the watch to attach", 5*time.Second, func() bool { return changes.Load() > 0 })

	daemon.OpenRequest(1, codexCommandApproval, approvalOn(managedThreadID, "call_a"))
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": waitingStatus("waitingOnApproval")})
	testkit.Eventually(t, "the watch to subscribe to the waiting thread", 5*time.Second, func() bool { return resumesOf(daemon, managedThreadID) == 1 })

	before := changes.Load()
	daemon.OpenRequest(2, codexCommandApproval, approvalOn(managedThreadID, "call_b"))
	testkit.Eventually(t, "a second request to wake the catalog", 5*time.Second, func() bool { return changes.Load() > before })

	before = changes.Load()
	daemon.Answer(2)
	testkit.Eventually(t, "one of two resolving to wake the catalog", 5*time.Second, func() bool { return changes.Load() > before })

	daemon.Answer(1)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": waitingStatus()})
	testkit.Eventually(t, "the watch to unsubscribe once the thread stops waiting", 5*time.Second, func() bool {
		return len(daemon.Calls("thread/unsubscribe")) == 1
	})
	if responses := daemon.Responses(); len(responses) != 0 {
		t.Fatalf("the watch answered a request: %v", responses)
	}
}

func TestTheStatusWatchSubscribesToAThreadAlreadyWaitingWhenItAttaches(t *testing.T) {
	fastTerminalPolls(t)
	daemon := startManagedCodex(t, nil)
	daemon.AddThread(runningThread(managedThreadID))
	daemon.Load(managedThreadID)
	daemon.OpenRequest(1, codexCommandApproval, approvalOn(managedThreadID, "call_a"))
	var changes atomic.Int32
	watchedCodexWaking(t, managedThreadID, func() { changes.Add(1) })
	testkit.Eventually(t, "the watch to subscribe to the thread waiting before it attached", 5*time.Second, func() bool {
		return resumesOf(daemon, managedThreadID) == 1
	})
	before := changes.Load()
	daemon.OpenRequest(2, codexCommandApproval, approvalOn(managedThreadID, "call_b"))
	testkit.Eventually(t, "a second request to wake the catalog", 5*time.Second, func() bool { return changes.Load() > before })
}

func TestRequestsHeardWhileTheWatchIsStillSubscribingWakeTheCatalog(t *testing.T) {
	watch := newRequestWatch(nil, nil)
	watch.want[managedThreadID], watch.busy[managedThreadID] = true, true
	opened := codexEvent{Method: codexCommandApproval, RequestID: json.RawMessage("2"), Params: map[string]any{"threadId": managedThreadID}}
	resolved := codexEvent{Method: codexRequestResolved, Params: map[string]any{"threadId": managedThreadID, "requestId": float64(1)}}
	elsewhere := codexEvent{Method: codexCommandApproval, RequestID: json.RawMessage("3"), Params: map[string]any{"threadId": otherManagedThreadID}}
	if !watch.heard(opened) || !watch.heard(resolved) || watch.heard(elsewhere) {
		t.Fatalf("opened=%v resolved=%v elsewhere=%v", watch.heard(opened), watch.heard(resolved), watch.heard(elsewhere))
	}
}

func TestAReloadDoesNotKeepTheWatchOnAThreadThatStoppedWaitingMeanwhile(t *testing.T) {
	fastTerminalPolls(t)
	reloading, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	daemon := startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		if method == codexThreadResume && params["excludeTurns"] == nil {
			once.Do(func() { close(reloading); <-proceed })
		}
		return nil
	})
	daemon.AddThread(runningThread(managedThreadID))
	daemon.Load(managedThreadID)
	daemon.OpenRequest(1, codexCommandApproval, approvalOn(managedThreadID, "call_a"))
	codex := watchedCodex(t, managedThreadID)
	testkit.Eventually(t, "the watch to subscribe to the waiting thread", 5*time.Second, func() bool {
		return resumesOf(daemon, managedThreadID) == 1 && daemon.Subscribers(managedThreadID) == 1
	})
	ctx := context.Background()
	reloader, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	daemon.CompleteTurn(managedThreadID, "done")
	done := make(chan error, 1)
	go func() {
		done <- codex.reloadThread(ctx, reloader, &surface.Session{ID: managedThreadID}, func() error { return nil })
	}()
	<-reloading
	daemon.Answer(1)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "idle"}})
	time.Sleep(200 * time.Millisecond)
	close(proceed)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = reloader.Close()
	testkit.Eventually(t, "no one to follow the thread once it stopped waiting", 5*time.Second, func() bool {
		return daemon.Subscribers(managedThreadID) == 0
	})
}

func TestAThreadThatWaitsAgainWhileTheWatchLeavesItIsWatchedAgain(t *testing.T) {
	fastTerminalPolls(t)
	leaving, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	daemon := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/unsubscribe" {
			once.Do(func() { close(leaving); <-proceed })
		}
		return nil
	})
	daemon.AddThread(runningThread(managedThreadID))
	daemon.Load(managedThreadID)
	daemon.HoldElsewhere(managedThreadID, true)
	var changes atomic.Int32
	watchedCodexWaking(t, managedThreadID, func() { changes.Add(1) })
	daemon.OpenRequest(1, codexCommandApproval, approvalOn(managedThreadID, "call_a"))
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": waitingStatus("waitingOnApproval")})
	testkit.Eventually(t, "the watch to subscribe", 5*time.Second, func() bool { return resumesOf(daemon, managedThreadID) == 1 })

	daemon.Answer(1)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": waitingStatus()})
	<-leaving
	daemon.OpenRequest(2, codexCommandApproval, approvalOn(managedThreadID, "call_b"))
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": waitingStatus("waitingOnApproval")})
	time.Sleep(100 * time.Millisecond)
	close(proceed)
	testkit.Eventually(t, "the watch to subscribe again", 5*time.Second, func() bool {
		return resumesOf(daemon, managedThreadID) == 2 && daemon.Subscribers(managedThreadID) == 1
	})
	before := changes.Load()
	daemon.OpenRequest(3, codexCommandApproval, approvalOn(managedThreadID, "call_c"))
	testkit.Eventually(t, "a later request to wake the catalog", 5*time.Second, func() bool { return changes.Load() > before })
}

func TestTheStatusWatchNeverLoadsAThreadItsAppServerDoesNotHave(t *testing.T) {
	fastTerminalPolls(t)
	daemon := startManagedCodex(t, nil)
	daemon.AddThread(runningThread(managedThreadID))
	daemon.Load(managedThreadID)
	daemon.AddThread(runningThread(otherManagedThreadID))
	var changes atomic.Int32
	watchedCodexWaking(t, managedThreadID, func() { changes.Add(1) })
	testkit.Eventually(t, "the watch to attach", 5*time.Second, func() bool { return changes.Load() > 0 })
	daemon.Notify("thread/status/changed", map[string]any{"threadId": otherManagedThreadID, "status": waitingStatus("waitingOnApproval")})
	time.Sleep(300 * time.Millisecond)
	if resumes := resumesOf(daemon, otherManagedThreadID); resumes != 0 || daemon.Loaded(otherManagedThreadID) {
		t.Fatalf("the watch resumed a thread its app-server had not loaded: resumes=%d loaded=%v", resumes, daemon.Loaded(otherManagedThreadID))
	}
}
