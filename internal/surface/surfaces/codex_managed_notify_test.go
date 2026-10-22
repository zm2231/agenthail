package surfaces

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

// slowManagedPoll raises the stream's poll interval far above the bound the
// tests assert, so an event inside the bound can only come from a
// notification.
func slowManagedPoll(t *testing.T) {
	t.Helper()
	original := transcriptPollInterval
	transcriptPollInterval = 10 * time.Second
	t.Cleanup(func() { transcriptPollInterval = original })
}

const notifyBound = time.Second

func dialTestManagedCodex(t *testing.T) *managedCodexClient {
	t.Helper()
	client, err := dialManagedCodex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestAManagedRequestEndsOnItsContextOrTimeoutWithoutBlockingOthers(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	holding := make(chan struct{})
	var held atomic.Bool
	startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == "test/hold" && held.CompareAndSwap(false, true) {
			close(holding)
			<-release
		}
		return nil
	})
	client := dialTestManagedCodex(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.Request(ctx, "test/hold", map[string]any{}, time.Minute)
		done <- err
	}()
	<-holding
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled request err=%v", err)
		}
	case <-time.After(notifyBound):
		t.Fatal("a cancelled request kept waiting")
	}

	_, err := client.Request(context.Background(), "test/silent", map[string]any{}, 50*time.Millisecond)
	if err == nil || !isCodexTimeout(err) {
		t.Fatalf("timed-out request err=%v", err)
	}
	client.mu.Lock()
	waiting := len(client.waiters)
	client.mu.Unlock()
	if waiting != 0 {
		t.Fatalf("ended requests left %d waiters", waiting)
	}
}

func TestALostManagedConnectionFailsPendingAndLaterRequests(t *testing.T) {
	startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == "test/die" {
			return codextest.Drop
		}
		return nil
	})
	client := dialTestManagedCodex(t)
	done := make(chan error, 1)
	go func() {
		_, err := client.Request(context.Background(), "test/die", map[string]any{}, time.Minute)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a request on a lost connection succeeded")
		}
	case <-time.After(notifyBound):
		t.Fatal("a request on a lost connection kept waiting")
	}
	started := time.Now()
	if _, err := client.Request(context.Background(), "thread/loaded/list", map[string]any{}, time.Minute); err == nil || time.Since(started) > notifyBound {
		t.Fatalf("request after the loss err=%v after %s", err, time.Since(started))
	}
	select {
	case <-client.Notified():
		t.Fatal("a lost connection reported a notification")
	default:
	}
}

func TestThreadTurnsAreListedByTheTopLevelLimit(t *testing.T) {
	fake := startManagedCodex(t, managedThreadReads(managedTurn("turn", "completed", agentMessage("final", "final_answer", "done"))))
	codex := isolatedManagedRuntime(t)
	if _, err := codex.Observe(context.Background(), managedSession()); err != nil {
		t.Fatal(err)
	}
	if err := codex.Stream(context.Background(), managedSession(), "turn", func(surface.StreamEvent) {}, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	calls := fake.Calls("thread/turns/list")
	if len(calls) < 2 {
		t.Fatalf("turns/list calls=%v", calls)
	}
	for _, params := range calls {
		if _, paged := params["page"]; paged {
			t.Fatalf("turns/list nests its limit under page, which the app-server ignores: %v", params)
		}
		if limit, ok := params["limit"].(float64); !ok || limit < 1 {
			t.Fatalf("turns/list without a top-level limit: %v", params)
		}
	}
	if last := calls[len(calls)-1]; last["limit"] != float64(1) || last["itemsView"] != "full" {
		t.Fatalf("stream read=%v, want the newest turn with every item", last)
	}

	client := dialTestManagedCodex(t)
	if _, err := codex.activeTurnID(context.Background(), client, "thread"); err != nil {
		t.Fatal(err)
	}
	if _, err := codex.listThreadItems(context.Background(), client, "thread", "turn"); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"thread/turns/list", "thread/items/list"} {
		for _, params := range fake.Calls(method) {
			if _, paged := params["page"]; paged {
				t.Fatalf("%s nests its limit under page: %v", method, params)
			}
			if limit, ok := params["limit"].(float64); !ok || limit < 1 {
				t.Fatalf("%s without a top-level limit: %v", method, params)
			}
		}
	}
	if active := fake.Calls("thread/turns/list"); active[len(active)-1]["limit"] != float64(1) {
		t.Fatalf("active turn lookup=%v, want only the newest turn", active[len(active)-1])
	}
}

func TestOnlyTheAppServersFirstTurnRefusalsReadAsNoTurnsYet(t *testing.T) {
	for _, test := range []struct {
		code    int
		message string
		want    bool
	}{
		{-32600, "thread 01a163dd-6cbf-7c83-9637-51e60a7c3033 is not materialized yet; thread/turns/list is unavailable before first user message", true},
		{-32601, "list_turns is not supported yet", true},
		{-32601, "paginated_threads is not supported yet", true},
		{-32601, "Method not found", false},
		{-32600, "thread not found: 01a163dd-6cbf-7c83-9637-51e60a7c3033", false},
		{-32603, "list_turns is not supported yet", false},
	} {
		err := fmt.Errorf("thread/turns/list: %w", &codexRPCError{Method: "thread/turns/list", Code: test.code, Message: test.message})
		if got := codexTurnsNotListedYet(err); got != test.want {
			t.Errorf("%d %q: got %v, want %v", test.code, test.message, got, test.want)
		}
	}
	if codexTurnsNotListedYet(errors.New("thread/turns/list RPC error (-32601): list_turns is not supported yet")) {
		t.Error("an error that is not the app-server's answer matched by its text")
	}
}

type switchableTurn struct {
	mu   sync.Mutex
	done bool
}

func (s *switchableTurn) respond(method string, params map[string]any) map[string]any {
	if method != "thread/read" && method != "thread/turns/list" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return managedReadAnswer(method, params, managedTurn("turn", "completed", agentMessage("final", "final_answer", "finished")))
	}
	return managedReadAnswer(method, params, managedTurn("turn", "inProgress", agentMessage("interim", "commentary", "working")))
}

func (s *switchableTurn) finish() {
	s.mu.Lock()
	s.done = true
	s.mu.Unlock()
}

func streamManagedInBackground(t *testing.T, onEvent func(surface.StreamEvent)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = isolatedManagedRuntime(t).Stream(ctx, managedSession(), "", onEvent, time.Minute)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
}

func awaitCalls(t *testing.T, fake *codextest.Daemon, method string, count int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		called := fake.CallMade()
		if len(fake.Calls(method)) >= count {
			return
		}
		select {
		case <-called:
		case <-deadline:
			t.Fatalf("%s called %d times, want %d", method, len(fake.Calls(method)), count)
		}
	}
}

func handledNotifications(t *testing.T) <-chan struct{} {
	t.Helper()
	handled := make(chan struct{}, 64)
	managedNotificationsHandled = func() {
		select {
		case handled <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { managedNotificationsHandled = nil })
	return handled
}

func TestAManagedStreamReadsAsSoonAsItsThreadReportsAChange(t *testing.T) {
	slowManagedPoll(t)
	handled := handledNotifications(t)
	turn := &switchableTurn{}
	fake := startManagedCodex(t, turn.respond)
	fake.AddThread(codextest.Thread{ID: "thread"})
	fake.Load("thread")
	done := make(chan struct{}, 1)
	streamManagedInBackground(t, func(event surface.StreamEvent) {
		if event.Kind == "done" {
			done <- struct{}{}
		}
	})
	awaitCalls(t, fake, "thread/resume", 1)
	if resume := fake.Calls("thread/resume")[0]; resume["excludeTurns"] != true {
		t.Fatalf("resume=%v, want the subscription without the thread's turns", resume)
	}
	awaitCalls(t, fake, "thread/turns/list", 2)

	reads := len(fake.Calls("thread/turns/list"))
	for len(handled) > 0 {
		<-handled
	}
	fake.Notify("item/agentMessage/delta", map[string]any{"threadId": "thread", "delta": "fin"})
	select {
	case <-handled:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream never handled the delta")
	}
	if got := len(fake.Calls("thread/turns/list")); got != reads {
		t.Fatalf("a text delta caused %d reads", got-reads)
	}

	turn.finish()
	finished := time.Now()
	fake.Notify("turn/completed", map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn"}})
	select {
	case <-done:
		t.Logf("the finished turn reached the stream after %s", time.Since(finished).Round(time.Millisecond))
	case <-time.After(notifyBound):
		t.Fatalf("the finished turn did not reach the stream within %s", notifyBound)
	}
}

func TestAManagedStreamSubscribesToAnUnloadedThreadOnceItBecomesActive(t *testing.T) {
	slowManagedPoll(t)
	turn := &switchableTurn{}
	fake := startManagedCodex(t, turn.respond)
	fake.AddThread(codextest.Thread{ID: "thread"})
	usage := make(chan *surface.ContextUsage, 4)
	streamManagedInBackground(t, func(event surface.StreamEvent) {
		if event.Kind == "context" && event.Context != nil {
			usage <- event.Context
		}
	})
	awaitCalls(t, fake, "thread/turns/list", 2)
	if calls := fake.Calls("thread/resume"); len(calls) != 0 || fake.Loaded("thread") {
		t.Fatalf("the stream loaded a thread that was not loaded: %v", calls)
	}

	fake.Load("thread")
	fake.Notify("thread/status/changed", map[string]any{"threadId": "thread", "status": map[string]any{"type": "active", "activeFlags": []any{}}})
	awaitCalls(t, fake, "thread/resume", 1)

	fake.Notify("thread/tokenUsage/updated", map[string]any{
		"threadId": "thread",
		"tokenUsage": map[string]any{
			"last":               map[string]any{"inputTokens": float64(1000), "totalTokens": float64(1200)},
			"total":              map[string]any{"totalTokens": float64(5000)},
			"modelContextWindow": float64(258400),
		},
	})
	select {
	case context := <-usage:
		if context.UsedTokens != 1200 || context.CumulativeTokens != 5000 {
			t.Fatalf("context=%+v", context)
		}
	case <-time.After(notifyBound):
		t.Fatal("token usage did not reach the subscribed stream")
	}
}

func TestAManagedStreamWaitsForTheFirstTurnOfANewThread(t *testing.T) {
	var mu sync.Mutex
	materialized := false
	refusals := []map[string]any{
		{"code": float64(-32600), "message": "thread thread is not materialized yet; thread/turns/list is unavailable before first user message"},
		{"code": float64(-32601), "message": "paginated_threads is not supported yet"},
	}
	reads := 0
	fake := startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		if method != "thread/turns/list" && method != "thread/read" {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		reads++
		if !materialized {
			return map[string]any{"error": refusals[min(reads-1, len(refusals)-1)]}
		}
		return managedReadAnswer(method, params, managedTurn("turn", "completed", agentMessage("final", "final_answer", "first answer")))
	})
	done := make(chan error, 1)
	var events []surface.StreamEvent
	go func() {
		done <- isolatedManagedRuntime(t).Stream(context.Background(), managedSession(), "", func(event surface.StreamEvent) {
			events = append(events, event)
		}, 5*time.Second)
	}()
	awaitCalls(t, fake, "thread/turns/list", 3)
	mu.Lock()
	materialized = true
	mu.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("stream of a new thread err=%v", err)
	}
	if len(events) != 2 || events[0].Text != "first answer" || events[1].Kind != "done" {
		t.Fatalf("events=%+v", events)
	}
}
