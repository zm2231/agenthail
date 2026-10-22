package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func hostRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func hostedSession(t *testing.T, claude *surfaces.Claude, id string) surface.Session {
	t.Helper()
	var found surface.Session
	claudehosttest.WaitFor(t, "the host session to be listed", func() bool {
		sessions, err := claude.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, session := range sessions {
			if session.ID == id && session.RuntimeKind == surface.RuntimeClaudeHost {
				found = session
				return true
			}
		}
		return false
	})
	return found
}

func turnOrigin(t *testing.T, turn claudehosttest.Turn) string {
	t.Helper()
	var origin struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(turn.Origin, &origin); err != nil {
		t.Fatal(err)
	}
	return origin.Kind
}

func TestAPersonsMessageToAHostSessionIsDeliveredUnderItsIntentUUID(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	claude := surfaces.NewClaude(nil, fixture.Home)
	session := hostedSession(t, claude, id)
	r := hostRegistry(t)
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, r, []surface.Surface{claude})
	ctx := context.Background()

	receipt, err := d.dispatcher().DeliverWithOptions(ctx, claude, &session, "sent now", "", surface.SendOptions{Author: surface.AuthorPerson})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Evidence != surface.EvidenceDelivered || receipt.TurnID != intent.UUID {
		t.Fatalf("receipt = %+v, want delivered with turn %s", receipt, intent.UUID)
	}

	queued, err := r.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: registry.OperatorSessionID, TargetSessionID: id, Message: "sent from the queue", Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.QueueDeliveryUsingIntent(queued.ID, "sent from the queue", "", surface.SendOptions{Author: surface.AuthorPerson}); err != nil {
		t.Fatal(err)
	}
	d.drainMessageQueue(ctx, claude, &session)

	claudehosttest.WaitFor(t, "both turns", func() bool { return len(fixture.Turns(t, id)) == 2 })
	turns := fixture.Turns(t, id)
	for i, want := range []struct{ uuid, text string }{{intent.UUID, "sent now"}, {queued.UUID, "sent from the queue"}} {
		if turns[i].UUID != want.uuid || turns[i].Text != want.text || turnOrigin(t, turns[i]) != "human" {
			t.Fatalf("turn %d = %+v, want a human turn %q under %s", i, turns[i], want.text, want.uuid)
		}
	}
}

func TestAStoppedDaemonLeavesNoClaudeHostSupervisorRunning(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var returned atomic.Bool
	manager := &claudehost.Manager{Home: t.TempDir(), Launch: func() (claudehost.Launch, error) {
		close(entered)
		<-release
		returned.Store(true)
		return claudehost.Launch{}, errors.New("no Claude host in this test")
	}}
	d := newTestDaemon(t, hostRegistry(t), nil)
	d.SetClaudeHost(manager)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d.Run(ctx)
	}()
	<-entered
	cancel()
	select {
	case <-done:
		close(release)
		t.Fatal("Run returned while its Claude host supervisor was still running")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-done
	if !returned.Load() {
		t.Fatal("Run returned before its Claude host supervisor finished")
	}
}

func TestARestartedDaemonAttachesToTheRunningHostAndKeepsItsSessions(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	r := hostRegistry(t)
	run := func(manager *claudehost.Manager) (stop func()) {
		d := newTestDaemon(t, r, nil)
		d.SetClaudeHost(manager)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = d.Run(ctx)
		}()
		return func() {
			cancel()
			<-done
		}
	}
	client := claudehost.NewClient(fixture.Home)
	hello := func(expected int) claudehost.Hello {
		t.Helper()
		var lastErr error
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			got, err := client.Hello(ctx)
			cancel()
			if err == nil {
				return got
			}
			lastErr = err
		}
		t.Fatalf("timed out waiting for the host\n%s", fixture.Diagnose(expected, lastErr))
		return claudehost.Hello{}
	}

	stop := run(fixture.Manager(t, "1.0.0"))
	first := hello(0)
	failf := func(format string, args ...any) {
		t.Helper()
		t.Fatalf(format+"\n%s", append(args, fixture.Diagnose(first.PID, nil))...)
	}
	id := uuid.NewString()
	opened := fixture.Open(t, client, id)
	stop()

	if !claudehosttest.Alive(first.PID) || !claudehosttest.Alive(opened.PID) {
		failf("the host and its session outlive the daemon")
	}
	if alone := hello(first.PID); alone.PID != first.PID {
		failf("with no daemon running, host %d answered, want %d", alone.PID, first.PID)
	}
	stop = run(fixture.Manager(t, "1.0.0"))
	defer stop()
	time.Sleep(300 * time.Millisecond)
	if again := hello(first.PID); again.PID != first.PID {
		failf("restarted daemon reached host %d, want the running host %d", again.PID, first.PID)
	}
	lock, err := os.Open(filepath.Join(fixture.Home, ".agenthail", "claude-host", "lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != syscall.EWOULDBLOCK {
		failf("host lock = %v, want held by the one running host", err)
	}
	listing, err := client.List(context.Background())
	if err != nil {
		failf("list: %v", err)
	}
	if len(listing.Sessions) != 1 || listing.Sessions[0].SessionID != id || listing.Sessions[0].PID != opened.PID {
		failf("sessions = %+v, want %s still on pid %d", listing.Sessions, id, opened.PID)
	}
	log, err := os.ReadFile(filepath.Join(fixture.Home, ".agenthail", "claude-host", "host.log"))
	if err != nil {
		t.Fatal(err)
	}
	if starts := strings.Count(string(log), "listening"); starts != 1 {
		failf("host started %d times", starts)
	}
}

func TestAHostThatExitsWhileRunningIsLoggedWithItsLogPath(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	d := newTestDaemon(t, hostRegistry(t), nil)
	manager := fixture.Manager(t, "test")
	d.SetClaudeHost(manager)
	var logged strings.Builder
	d.log = log.New(&logged, "", 0)
	ctx := context.Background()

	first := d.checkClaudeHost(ctx, claudeHostCheck{})
	if again := d.checkClaudeHost(ctx, first); again != first {
		t.Fatalf("second check = %+v, want the same host %+v", again, first)
	}
	if logged.String() != "Claude host: running\n" {
		t.Fatalf("log while the host runs:\n%s", logged.String())
	}

	fixture.KillHost(t)
	replaced := d.checkClaudeHost(ctx, first)
	if replaced.status != "running" || replaced.pid == first.pid {
		t.Fatalf("check after the host exited = %+v, want a new running host", replaced)
	}
	want := fmt.Sprintf("Claude host: pid %d is gone and pid %d now runs; see %s\n", first.pid, replaced.pid, manager.LogPath())
	if !strings.HasSuffix(logged.String(), want) {
		t.Fatalf("log after the host exited:\n%s\nwant it to end with:\n%s", logged.String(), want)
	}
}

// A copy of an update that stranded a session: a protocol 1 host ran it, its
// Claude asked a person for approval on a turn it started itself, and the old
// daemon's drain closed its input. Claude refused the prompt and kept
// running, and the host kept listing the prompt. Installing this build stops
// that host; the dead prompt stops showing, an answer to it clears it, and
// once the stranded Claude exits the next message resumes the conversation in
// the new host and is answered.
func TestAnUpdateFromAProtocol1HostClearsItsDeadRequestAndResumesTheSession(t *testing.T) {
	for _, test := range []struct{ name, prompt string }{{"approval", "TOOL"}, {"question", "QUESTION"}} {
		t.Run(test.name, func(t *testing.T) { updateFromAProtocol1Host(t, test.prompt) })
	}
}

func updateFromAProtocol1Host(t *testing.T, prompt string) {
	fixture := claudehosttest.New(t, "")
	ctx := context.Background()
	// Reaching a conversation reads Claude's background jobs; there are none.
	agents := filepath.Join(fixture.Home, "claude-agents")
	if err := os.WriteFile(agents, []byte("#!/bin/sh\necho '[]'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", agents)
	fixture.StartProtocol1Host(t, "0.2.44")
	client := claudehost.NewClient(fixture.Home)
	id := uuid.NewString()
	opened := fixture.Open(t, client, id)
	claude := surfaces.NewClaude(nil, fixture.Home)
	session := hostedSession(t, claude, id)
	r := hostRegistry(t)
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, r, []surface.Surface{claude})
	d.log = log.New(io.Discard, "", 0)

	fixture.Peer(t, opened.PID, prompt)
	var request string
	claudehosttest.WaitFor(t, "the session to wait on the request", func() bool {
		observation, err := d.observeTurn(ctx, claude, &session)
		if err != nil || observation.WaitingOn == nil {
			return false
		}
		request = observation.WaitingOn.ID
		return true
	})
	if err := client.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "Claude to refuse the prompt", func() bool {
		turns := fixture.Turns(t, id)
		return len(turns) == 1 && strings.Contains(turns[0].Reply, ":error:")
	})
	if listing, err := client.List(ctx); err != nil || len(listing.Sessions) != 1 || len(listing.Sessions[0].Requests) != 1 {
		t.Fatalf("protocol 1 listing after the drain = %+v, %v; want the dead prompt still listed", listing, err)
	}

	d.SetClaudeHost(fixture.Manager(t, "0.2.48"))
	if check := d.checkClaudeHost(ctx, claudeHostCheck{}); check.status != "running" {
		t.Fatalf("host after the install = %+v", check)
	}
	if !claudehosttest.Alive(opened.PID) {
		t.Fatal("the stranded Claude was stopped by the install")
	}
	answer := httptest.NewRecorder()
	decision := surface.Decision{Behavior: "allow"}
	if prompt == "QUESTION" {
		decision.Answers = map[string]string{"Which color?": "Blue"}
	}
	d.respond(ctx, answer, claude, &session, request, decision, "the test")
	if answer.Code != http.StatusConflict {
		t.Fatalf("answer to the dead prompt = %d %s, want 409", answer.Code, answer.Body)
	}
	if waiting, _, err := r.WaitingState(id); err != nil || waiting != nil {
		t.Fatalf("waiting after the stale answer = %+v, %v; want none", waiting, err)
	}
	state, err := claude.Requests(ctx, &session)
	if err != nil || len(state.Requests) != 0 {
		t.Fatalf("requests after the install = %+v, %v; want none", state, err)
	}

	queued, err := d.dispatcher().DeliverWithOptions(ctx, claude, &session, "while it runs", "", surface.SendOptions{Author: surface.AuthorPerson})
	if err != nil || queued.Evidence != surface.EvidenceQueued {
		t.Fatalf("a message while the stranded Claude runs = %+v, %v; want queued", queued, err)
	}
	time.Sleep(300 * time.Millisecond)
	if turns := fixture.Turns(t, id); len(turns) != 1 {
		t.Fatalf("turns while the stranded Claude runs = %+v, want none new", turns)
	}
	_ = syscall.Kill(opened.PID, syscall.SIGTERM)
	claudehosttest.WaitFor(t, "the stranded Claude to exit", func() bool { return !claudehosttest.Alive(opened.PID) })
	receipt, err := d.dispatcher().DeliverWithOptions(ctx, claude, &session, "after it exited", "", surface.SendOptions{Author: surface.AuthorPerson})
	if err != nil || receipt.Evidence != surface.EvidenceDelivered {
		t.Fatalf("the next message after the stranded Claude exited = %+v, %v; want delivered", receipt, err)
	}
	claudehosttest.WaitFor(t, "the answer from the new host", func() bool {
		turns := fixture.Turns(t, id)
		return len(turns) == 2 && turns[1].Reply == "echo:after it exited" && turns[1].PID != opened.PID
	})
}

// After an update the daemon's request watcher follows the new host, while
// the retiring one still runs a session waiting on a person: a request the
// new host opens asks for a catalog pass at once.
func TestTheRequestWatcherFollowsTheNewHostWhileTheOldOneRetires(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := fixture.Manager(t, "1.0.0").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := client.Hello(ctx)
	waiting := uuid.NewString()
	opened := fixture.Open(t, client, waiting)
	fixture.Peer(t, opened.PID, "TOOL")
	d := newTestDaemon(t, hostRegistry(t), nil)
	d.log = log.New(io.Discard, "", 0)
	d.SetClaudeHost(fixture.Manager(t, "1.0.0"))
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		d.watchClaudeHostRequests(ctx)
	}()
	defer func() {
		cancel()
		<-watched
	}()
	select {
	case <-d.catalogNudge:
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher did not report the old host's request")
	}

	if client, err = fixture.Manager(t, "1.1.0").Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	fresh := uuid.NewString()
	if _, err := client.Open(ctx, claudehost.OpenRequest{SessionID: fresh, Cwd: fixture.Cwd}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(claudeHostWatchRetryWait + 500*time.Millisecond)
	for quiet := false; !quiet; {
		select {
		case <-d.catalogNudge:
		case <-time.After(300 * time.Millisecond):
			quiet = true
		}
	}
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: fresh, UUID: uuid.NewString(), Text: "TOOL"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-d.catalogNudge:
	case <-time.After(5 * time.Second):
		t.Fatal("a request on the new host asked for no catalog pass")
	}
	if !claudehosttest.Alive(old.PID) {
		t.Fatal("the old host left while its session waited on a person")
	}
}

// A protocol 2 host retiring under this build's daemon keeps the prompt its
// session waits on answerable: the daemon lists it, the person's answer
// reaches Claude through the old host, and the session's next message after
// the old host leaves with it resumes the conversation on this build's host.
func TestAnUpdateFromAProtocol2HostAnswersItsPromptThroughTheNewDaemon(t *testing.T) {
	for _, test := range []struct {
		name, prompt, reply string
		decision            surface.Decision
	}{
		{name: "approval", prompt: "TOOL", decision: surface.Decision{Behavior: "allow"}, reply: "tool:allow:"},
		{name: "question", prompt: "QUESTION", decision: surface.Decision{Behavior: "allow", Answers: map[string]string{"Which color?": "Blue"}}, reply: `question:allow:{"Which color?":"Blue"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := claudehosttest.New(t, "")
			ctx := context.Background()
			agents := filepath.Join(fixture.Home, "claude-agents")
			if err := os.WriteFile(agents, []byte("#!/bin/sh\necho '[]'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AGENTHAIL_CLAUDE_BIN", agents)
			old := fixture.StartProtocol2Host(t, "0.2.50")
			client := claudehost.NewClient(fixture.Home)
			id := uuid.NewString()
			opened := fixture.Open(t, client, id)
			claude := surfaces.NewClaude(nil, fixture.Home)
			session := hostedSession(t, claude, id)
			r := hostRegistry(t)
			if err := r.RegisterSession(session); err != nil {
				t.Fatal(err)
			}
			d := newTestDaemon(t, r, []surface.Surface{claude})
			d.log = log.New(io.Discard, "", 0)
			fixture.Peer(t, opened.PID, test.prompt)
			claudehosttest.WaitFor(t, "the session to wait on the request", func() bool {
				observation, err := d.observeTurn(ctx, claude, &session)
				return err == nil && observation.WaitingOn != nil
			})

			d.SetClaudeHost(fixture.Manager(t, "0.2.51"))
			if check := d.checkClaudeHost(ctx, claudeHostCheck{}); check.status != "running" || check.pid == old.PID {
				t.Fatalf("host after the install = %+v", check)
			}
			if hello, err := client.Hello(ctx); err != nil || hello.Protocol != claudehost.Protocol {
				t.Fatalf("hello after the install = %+v, %v", hello, err)
			}
			state, err := claude.Requests(ctx, &session)
			if err != nil || len(state.Requests) != 1 {
				t.Fatalf("requests through the retiring protocol 2 host = %+v, %v", state, err)
			}
			answer := httptest.NewRecorder()
			d.respond(ctx, answer, claude, &session, state.Requests[0].ID, test.decision, "the test")
			if answer.Code != http.StatusOK {
				t.Fatalf("answer through the retiring protocol 2 host = %d %s", answer.Code, answer.Body)
			}
			claudehosttest.WaitFor(t, "the answer to reach Claude and the old host to leave with its session", func() bool {
				turns := fixture.Turns(t, id)
				return len(turns) == 1 && turns[0].Reply == test.reply && !claudehosttest.Alive(old.PID) && !claudehosttest.Alive(opened.PID)
			})
			if waiting, _, err := r.WaitingState(id); err != nil || waiting != nil {
				t.Fatalf("waiting after the answer = %+v, %v; want none", waiting, err)
			}

			receipt, err := d.dispatcher().DeliverWithOptions(ctx, claude, &session, "after the update", "", surface.SendOptions{Author: surface.AuthorPerson})
			if err != nil || receipt.Evidence != surface.EvidenceDelivered {
				t.Fatalf("the next message = %+v, %v; want delivered", receipt, err)
			}
			claudehosttest.WaitFor(t, "the answer from this build's host", func() bool {
				turns := fixture.Turns(t, id)
				return len(turns) == 2 && turns[1].Reply == "echo:after the update" && turns[1].PID != opened.PID
			})
		})
	}
}

func TestAMessageClaudeHoldsIsADeliveryProblemAndItsSenderIsTold(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_HOLD_MS", "3000")
	fixture := claudehosttest.New(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := fixture.Start(t)
	sender, target := uuid.NewString(), uuid.NewString()
	fixture.Open(t, client, sender)
	fixture.OpenInMode(t, client, target, "bypassPermissions")
	claude := surfaces.NewClaude(nil, fixture.Home)
	senderSession, targetSession := hostedSession(t, claude, sender), hostedSession(t, claude, target)
	r := hostRegistry(t)
	for _, session := range []surface.Session{senderSession, targetSession} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d := newTestDaemon(t, r, []surface.Surface{claude})
	d.log = log.New(io.Discard, "", 0)
	d.SetClaudeHost(fixture.Manager(t, "test"))
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		d.watchClaudeHostRequests(ctx)
	}()
	defer func() {
		cancel()
		<-watched
	}()

	receipt, err := d.dispatcher().DeliverWithOptions(ctx, claude, &targetSession, "from a prompting session", "", surface.SendOptions{SourceSessionID: sender})
	if err != nil {
		t.Fatal(err)
	}
	problem := func(status registry.DeliveryIntentStatus) *registry.DeliveryProblem {
		problems, err := r.ListDeliveryProblems()
		if err != nil {
			t.Fatal(err)
		}
		for _, problem := range problems {
			if problem.DeliveryID == receipt.DeliveryID && problem.Status == status {
				return &problem
			}
		}
		return nil
	}
	claudehosttest.WaitFor(t, "the held message to be a delivery problem", func() bool { return problem(registry.DeliveryIntentHeld) != nil })
	if held := problem(registry.DeliveryIntentHeld); held.SessionID != target || !strings.Contains(held.Reason, "permission") {
		t.Fatalf("problem = %+v, want the target's held message with its cause", held)
	}
	told := func(word string) bool {
		rows, err := r.ListQueue(false, d.adapters())
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.SessionID == sender && strings.Contains(row.Message, word) {
				return true
			}
		}
		return false
	}
	if !told("held") {
		t.Fatal("the sender was not told its message is held")
	}
	claudehosttest.WaitFor(t, "the hold to expire", func() bool { return problem(registry.DeliveryIntentFailed) != nil })
	if !told("failure") {
		t.Fatal("the sender was not told its message was dropped")
	}
	if turns := fixture.Turns(t, target); len(turns) != 0 {
		t.Fatalf("turns = %+v, want none", turns)
	}
}

func TestASessionStreamOpenedOnANewHostSessionDeliversItsFirstReply(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	claude := surfaces.NewClaude(nil, fixture.Home)
	session := hostedSession(t, claude, id)
	r := hostRegistry(t)
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, r, []surface.Surface{claude})
	t.Cleanup(d.sources.shutdown)
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/session-stream?id="+id+"&after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("a session stream opened before the first turn is refused: status %d %s", response.StatusCode, body)
	}
	path := filepath.Join(fixture.Home, ".claude", "projects", "a-folder-claude-named", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","uuid":"u1","message":{"content":"say hello"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"the first reply"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("the first reply never reached the open stream: %v", err)
		}
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, "the first reply") {
			return
		}
	}
}

func newHostedSourceFixture(t *testing.T) (*surfaces.Claude, surface.Session, *registry.Registry, string) {
	t.Helper()
	fixture := claudehosttest.New(t, "")
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	claude := surfaces.NewClaude(nil, fixture.Home)
	session := hostedSession(t, claude, id)
	r := hostRegistry(t)
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	return claude, session, r, filepath.Join(fixture.Home, ".claude", "projects", "a-folder-claude-named", id+".jsonl")
}

func writeFirstTurn(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","uuid":"u1","message":{"content":"say hello"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"the first reply"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
}

func detachedSource(manager *sessionSourceManager, session surface.Session, adapter surface.Surface) *sessionSource {
	return &sessionSource{manager: manager, session: session, adapter: adapter, epoch: "seed", ctx: context.Background(), appendBodies: map[string]string{}, subscribers: map[uint64]chan registry.SessionJournalEntry{}}
}

func TestAViewerThatReconnectsBeforeTheFirstTurnStillGetsTheFirstReply(t *testing.T) {
	claude, session, r, path := newHostedSourceFixture(t)
	manager := newSessionSourceManager(r)
	if err := detachedSource(manager, session, claude).loadSeed(); err != nil {
		t.Fatal(err)
	}
	again := detachedSource(manager, session, claude)
	if err := again.loadSeed(); err != nil || !again.session.TranscriptFromStart {
		t.Fatalf("a source started again before the first turn: err=%v fromStart=%v", err, again.session.TranscriptFromStart)
	}
	writeFirstTurn(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	replied := make(chan struct{}, 1)
	streamed := again.session
	go claude.Stream(ctx, &streamed, "", func(event surface.StreamEvent) {
		if event.Text == "the first reply" {
			select {
			case replied <- struct{}{}:
			default:
			}
		}
	}, 8*time.Second)
	select {
	case <-replied:
	case <-ctx.Done():
		t.Fatal("the first reply never reached the source started again")
	}
}

func TestAHostedTranscriptThatGoesMissingIsNotReadAsNewByTheNextSource(t *testing.T) {
	claude, session, r, path := newHostedSourceFixture(t)
	manager := newSessionSourceManager(r)
	t.Cleanup(manager.shutdown)
	subscription, err := manager.subscribe(&session, claude)
	if err != nil {
		t.Fatal(err)
	}
	writeFirstTurn(t, path)
	deadline := time.After(10 * time.Second)
	for streamed := false; !streamed; {
		select {
		case entry := <-subscription.Entries:
			streamed = strings.Contains(string(entry.Payload), "the first reply")
		case <-deadline:
			t.Fatal("the source never streamed the new transcript")
		}
	}
	subscription.Cancel()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	next := detachedSource(manager, session, claude)
	if err := next.loadSeed(); err == nil || !strings.Contains(err.Error(), "transcript is unavailable") || next.session.TranscriptFromStart {
		t.Fatalf("a transcript that went missing is reported, not read as new: err=%v fromStart=%v", err, next.session.TranscriptFromStart)
	}
}
