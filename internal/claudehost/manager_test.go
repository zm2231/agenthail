package claudehost_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/testkit"
	"github.com/zm2231/agenthail/internal/testnode"
)

func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func uuids(turns []claudehosttest.Turn) []string {
	var out []string
	for _, turn := range turns {
		out = append(out, turn.UUID)
	}
	return out
}

func lockHost(home string) error {
	file, err := os.Open(filepath.Join(home, ".agenthail", "claude-host", "lock"))
	if err != nil {
		return err
	}
	defer file.Close()
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func TestEnsureStartsOneHostAndARestartedDaemonAttachesToIt(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	first := f.Manager(t, "1.0.0")
	client, err := first.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hello, err := client.Hello(ctx)
	if err != nil || hello.Protocol != claudehost.Protocol || hello.HostVersion != "1.0.0" {
		t.Fatalf("hello = %+v, %v", hello, err)
	}
	sessionID := uuid.NewString()
	opened, err := client.Open(ctx, claudehost.OpenRequest{SessionID: sessionID, Cwd: f.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	restarted := f.Manager(t, "1.0.0")
	client, err = restarted.Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := client.Hello(ctx)
	if err != nil || again.PID != hello.PID {
		t.Fatalf("restarted daemon reached host %+v, %v; want the running host pid %d", again, err, hello.PID)
	}
	if err := lockHost(f.Home); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("host lock while the host runs: %v, want EWOULDBLOCK", err)
	}
	listing, err := client.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Sessions) != 1 || listing.Sessions[0].SessionID != sessionID || listing.Sessions[0].PID != opened.PID {
		t.Fatalf("sessions after daemon restart = %+v, want %s on pid %d", listing.Sessions, sessionID, opened.PID)
	}
	message := uuid.NewString()
	if result, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: message, Text: "after restart", Origin: &claudehost.Origin{Kind: "human"}}); err != nil || !result.Accepted || result.Duplicate {
		t.Fatalf("send = %+v, %v", result, err)
	}
	testkit.Eventually(t, "the turn", 15*time.Second, func() bool { return slices.Equal(uuids(f.Turns(t, sessionID)), []string{message}) })
}

func TestDaemonsEnsuringAtOnceShareOneHost(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	pids := make(chan int, 8)
	var wg sync.WaitGroup
	for range cap(pids) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, err := f.Manager(t, "1.0.0").Ensure(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			hello, err := client.Hello(ctx)
			if err != nil {
				t.Error(err)
				return
			}
			pids <- hello.PID
		}()
	}
	wg.Wait()
	close(pids)
	seen := map[int]bool{}
	for pid := range pids {
		seen[pid] = true
	}
	if len(seen) != 1 {
		t.Fatalf("hosts = %v, want one", seen)
	}
	if running := f.HostPIDs(t); len(running) != 1 {
		t.Fatalf("host processes = %v, want one", running)
	}
	if starts := strings.Count(hostLog(t, f.Home), "listening"); starts != 1 {
		t.Fatalf("host started %d times", starts)
	}
}

func TestEnsureStartsANewHostAfterOneCrashed(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	client := f.Start(t)
	first, err := client.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.KillHost(t)
	if _, err := os.Stat(claudehost.SocketPath(f.Home)); err != nil {
		t.Fatalf("a crashed host leaves its socket file: %v", err)
	}
	client, err = f.Manager(t, "test").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := client.Hello(ctx); err != nil || again.PID == first.PID {
		t.Fatalf("hello after the crash = %+v, %v; want a new host", again, err)
	}
}

func TestEnsureTakesTheLockWhenItsHolderExitsBeforeItsHostAnswers(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	dir := filepath.Join(f.Home, ".agenthail", "claude-host")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	holder, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	taken := make(chan struct{})
	var once sync.Once
	t.Cleanup(claudehost.OnLockTaken(func() { once.Do(func() { close(taken) }) }))
	done := make(chan error, 1)
	go func() {
		_, err := f.Manager(t, "test").Ensure(ctx)
		done <- err
	}()
	<-taken
	holder.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ensure did not take the released lock")
	}
	if starts := strings.Count(hostLog(t, f.Home), "listening"); starts != 1 {
		t.Fatalf("host started %d times", starts)
	}
}

func hostLog(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".agenthail", "claude-host", "host.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// listed waits until the hosts list the session with exactly the requests
// named and returns its listing.
func listed(t *testing.T, client claudehost.Client, sessionID string, requests int) claudehost.Session {
	t.Helper()
	var found claudehost.Session
	testkit.Eventually(t, fmt.Sprintf("session %s listed with %d requests", sessionID, requests), 15*time.Second, func() bool {
		listing, err := client.List(context.Background())
		if err != nil {
			return false
		}
		for _, session := range listing.Sessions {
			if session.SessionID == sessionID && len(session.Requests) == requests {
				found = session
				return true
			}
		}
		return false
	})
	return found
}

func replies(turns []claudehosttest.Turn) []string {
	var out []string
	for _, turn := range turns {
		out = append(out, turn.Reply)
	}
	return out
}

// An update replaces the host while a session waits on a person: Claude
// started the turn itself, from another agent's message, so the host never
// saw it begin. The approval or question stays answerable, the answer reaches
// Claude, the session stays reachable, and the old host leaves once the
// session is idle.
func TestReplacingTheHostKeepsAPendingRequestAnswerableAndItsSessionReachable(t *testing.T) {
	for _, test := range []struct {
		name, prompt, reply string
		decision            claudehost.Decision
		outcome             string
		check               func(claudehost.Request) bool
	}{
		{name: "approval", prompt: "TOOL", decision: claudehost.Decision{Behavior: "allow"}, outcome: "allowed", reply: "tool:allow:",
			check: func(r claudehost.Request) bool { return r.Kind == "approval" && r.Tool == "Bash" }},
		{name: "question", prompt: "QUESTION", decision: claudehost.Decision{Behavior: "allow", Answers: map[string]string{"Which color?": "Blue"}}, outcome: "answered", reply: `question:allow:{"Which color?":"Blue"}`,
			check: func(r claudehost.Request) bool {
				return r.Kind == "question" && len(r.Questions) == 1 && r.Questions[0].Question == "Which color?" && len(r.Questions[0].Options) == 2 && r.Questions[0].Options[1].Label == "Blue"
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := claudehosttest.New(t, "")
			ctx := testContext(t)
			client, err := f.Manager(t, "1.0.0").Ensure(ctx)
			if err != nil {
				t.Fatal(err)
			}
			oldHello, _ := client.Hello(ctx)
			sessionID := uuid.NewString()
			opened := f.Open(t, client, sessionID)
			f.Peer(t, opened.PID, test.prompt)
			request := listed(t, client, sessionID, 1).Requests[0]

			client, err = f.Manager(t, "1.1.0").Ensure(ctx)
			if err != nil {
				t.Fatalf("Ensure after the update = %v, want this version's host started", err)
			}
			if hello, err := client.Hello(ctx); err != nil || hello.HostVersion != "1.1.0" || hello.PID == oldHello.PID {
				t.Fatalf("hello after the update = %+v, %v", hello, err)
			}
			got := listed(t, client, sessionID, 1)
			if got.PID != opened.PID || got.Requests[0].RequestID != request.RequestID || !test.check(got.Requests[0]) {
				t.Fatalf("listing after the update = %+v, want pid %d still asking %+v", got, opened.PID, request)
			}
			message := uuid.NewString()
			if result, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: message, Text: "after the update", Origin: &claudehost.Origin{Kind: "human"}}); err != nil || !result.Accepted {
				t.Fatalf("send after the update = %+v, %v", result, err)
			}
			if outcome, err := client.Respond(ctx, sessionID, request.RequestID, test.decision); err != nil || outcome != test.outcome {
				t.Fatalf("respond after the update = %q, %v; want %s", outcome, err, test.outcome)
			}
			testkit.Eventually(t, "the old host to finish the turns and leave", 15*time.Second, func() bool {
				return !claudehosttest.Alive(oldHello.PID) && !claudehosttest.Alive(opened.PID)
			})
			if got := replies(f.Turns(t, sessionID)); !slices.Equal(got, []string{test.reply, "echo:after the update"}) {
				t.Fatalf("turns = %v, want the answer to reach Claude and then the message", got)
			}
			if retiring := client.Retiring(); len(retiring) != 0 {
				t.Fatalf("retiring hosts after the old one left = %v", retiring)
			}

			resumed, err := client.Open(ctx, claudehost.OpenRequest{Resume: sessionID, Cwd: f.Cwd})
			if err != nil || resumed.PID == opened.PID {
				t.Fatalf("resume on the new host = %+v, %v", resumed, err)
			}
			if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: uuid.NewString(), Text: "on the new host"}); err != nil {
				t.Fatal(err)
			}
			testkit.Eventually(t, "the turn on the new host", 15*time.Second, func() bool { return len(f.Turns(t, sessionID)) == 3 })
		})
	}
}

// Repeated updates retire one host each, and each leaves once its own
// sessions are idle: a host never outlives the sessions it had.
func TestRetiredHostsLeaveWithTheirSessions(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	client, err := f.Manager(t, "1").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := client.Hello(ctx)
	sessionID := uuid.NewString()
	opened := f.Open(t, client, sessionID)
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: uuid.NewString(), Text: "SLOW 60000"}); err != nil {
		t.Fatal(err)
	}
	if client, err = f.Manager(t, "2").Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	second, _ := client.Hello(ctx)
	if client, err = f.Manager(t, "3").Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the host without sessions to leave", 15*time.Second, func() bool { return !claudehosttest.Alive(second.PID) })
	if !claudehosttest.Alive(first.PID) {
		t.Fatal("the first host left while its session was busy")
	}
	if retiring := client.Retiring(); len(retiring) != 1 {
		t.Fatalf("retiring hosts = %v, want only the one with a busy session", retiring)
	}
	if err := client.Interrupt(ctx, sessionID); err != nil {
		t.Fatalf("interrupt the turn through the retiring host = %v", err)
	}
	testkit.Eventually(t, "the first host to leave after the turn", 15*time.Second, func() bool {
		return !claudehosttest.Alive(first.PID) && !claudehosttest.Alive(opened.PID)
	})
	if hosts := f.HostPIDs(t); len(hosts) != 1 {
		t.Fatalf("hosts = %v, want only the current one", hosts)
	}
}

// A host from before hosts could retire is stopped by the update. The Claude
// processes it ran keep running, held from the new host, until they exit.
func TestEnsureStopsAProtocol1HostAndLeavesItsClaudeRunning(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	old := f.StartProtocol1Host(t, "0.2.44")
	client := claudehost.NewClient(f.Home)
	sessionID := uuid.NewString()
	opened := f.Open(t, client, sessionID)

	client, err := f.Manager(t, "0.2.48").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hello, err := client.Hello(ctx); err != nil || hello.Protocol != claudehost.Protocol || hello.PID == old.PID {
		t.Fatalf("hello after the update = %+v, %v", hello, err)
	}
	testkit.Eventually(t, "the protocol 1 host to exit", 15*time.Second, func() bool { return !claudehosttest.Alive(old.PID) })
	if !claudehosttest.Alive(opened.PID) {
		t.Fatal("the Claude the protocol 1 host ran was stopped with it")
	}
	listing, err := client.List(ctx)
	if err != nil || len(listing.Orphans) != 1 || listing.Orphans[0].SessionID != sessionID {
		t.Fatalf("listing = %+v, %v; want the session held as an orphan", listing, err)
	}
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: uuid.NewString(), Text: "x"}); !claudehost.IsCode(err, "held") {
		t.Fatalf("send while the old Claude runs = %v, want held", err)
	}
}

func TestClientDistinguishesNoHostFromARefusal(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	if _, err := claudehost.NewClient(f.Home).Hello(ctx); !errors.Is(err, claudehost.ErrNotRunning) {
		t.Fatalf("hello without a host = %v, want claudehost.ErrNotRunning", err)
	}
	client, err := f.Manager(t, "1.0.0").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(ctx, claudehost.SendRequest{SessionID: uuid.NewString(), UUID: uuid.NewString(), Text: "x"})
	if !claudehost.IsCode(err, "not_open") {
		t.Fatalf("send to a session the host does not run = %v, want not_open", err)
	}
}

func TestSubscribeStreamsEventsBoundToTheMessage(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := testContext(t)
	client, err := f.Manager(t, "1.0.0").Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	if _, err := client.Open(ctx, claudehost.OpenRequest{SessionID: sessionID, Cwd: f.Cwd}); err != nil {
		t.Fatal(err)
	}
	message := uuid.NewString()
	events := make(chan claudehost.Event, 64)
	subscribed, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _, _ = client.Subscribe(subscribed, 0, func(e claudehost.Event) { events <- e }) }()
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: message, Text: "TOOL"}); err != nil {
		t.Fatal(err)
	}
	var request *claudehost.Request
	for request == nil {
		select {
		case e := <-events:
			if e.Kind == "request_opened" && e.SessionID == sessionID {
				request = e.Request
			}
		case <-ctx.Done():
			t.Fatal("no request_opened event")
		}
	}
	listing, err := client.List(ctx)
	if err != nil || len(listing.Sessions) != 1 || len(listing.Sessions[0].Requests) != 1 || listing.Sessions[0].Requests[0].RequestID != request.RequestID || request.Tool != "Bash" {
		t.Fatalf("listing = %+v, %v; request = %+v", listing, err, request)
	}
	outcome, err := client.Respond(ctx, sessionID, request.RequestID, claudehost.Decision{Behavior: "allow"})
	if err != nil || outcome != "allowed" {
		t.Fatalf("respond = %q, %v", outcome, err)
	}
	if _, err := client.Respond(ctx, sessionID, request.RequestID, claudehost.Decision{Behavior: "allow"}); !claudehost.IsCode(err, "stale") {
		t.Fatalf("second respond = %v, want stale", err)
	}
	var resolved, result bool
	for !resolved || !result {
		select {
		case e := <-events:
			resolved = resolved || (e.Kind == "request_resolved" && e.RequestID == request.RequestID && e.Outcome == "allowed")
			result = result || (e.Kind == "sdk" && strings.Contains(string(e.Message), `"type":"result"`) && strings.Contains(string(e.Message), message))
		case <-ctx.Done():
			t.Fatalf("events: resolved=%v result=%v", resolved, result)
		}
	}
}

func TestDiagnoseSignalsOnlyTheHost(t *testing.T) {
	fixture := claudehosttest.New(t, "")
	client := fixture.Start(t)
	hello, err := client.Hello(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	foreign := exec.Command(testnode.Path(t), "-e", "setInterval(() => {}, 1000)", "fake-claude.mjs")
	foreign.Env = append(os.Environ(), claudehosttest.OwnerEnv()...)
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = foreign.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = foreign.Process.Kill()
		<-exited
	})

	report := fixture.Diagnose(foreign.Process.Pid, nil)
	select {
	case <-exited:
		t.Fatalf("Diagnose ended a process that is not the host: %v", waitErr)
	case <-time.After(500 * time.Millisecond):
	}
	if strings.Contains(report, fmt.Sprintf("node report %d", foreign.Process.Pid)) {
		t.Fatalf("Diagnose reported on a process that is not the host:\n%s", report)
	}
	if !strings.Contains(report, fmt.Sprintf("node report %d\n{", hello.PID)) || !claudehosttest.Alive(hello.PID) {
		t.Fatalf("Diagnose did not report on the host %d:\n%s", hello.PID, report)
	}
}

// An update to a later protocol retires a protocol 2 host like any other, and
// this build's client keeps reaching the sessions it serves: their prompts
// are listed and answered, messages, mode changes and closes reach them (a
// close lets the running turn finish), and a
// resume returns the process it still runs. Once it leaves, the next resume
// opens the conversation on this build's host.
func TestALaterProtocolKeepsARetiringProtocol2HostServingItsSessions(t *testing.T) {
	for _, test := range []struct {
		name, prompt, reply string
		decision            claudehost.Decision
		outcome             string
	}{
		{name: "approval", prompt: "TOOL", decision: claudehost.Decision{Behavior: "allow"}, outcome: "allowed", reply: "tool:allow:"},
		{name: "question", prompt: "QUESTION", decision: claudehost.Decision{Behavior: "allow", Answers: map[string]string{"Which color?": "Blue"}}, outcome: "answered", reply: `question:allow:{"Which color?":"Blue"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := claudehosttest.New(t, "")
			ctx := testContext(t)
			// Its Claudes close their output and keep running until the test
			// releases them, so a stop finds the session ended while its Claude
			// still runs.
			lingering := filepath.Join(t.TempDir(), "linger-release")
			old := f.StartProtocol2Host(t, "0.2.50", "FAKE_CLAUDE_LINGER_RELEASE="+lingering)
			if old.Protocol != 2 {
				t.Fatalf("shipped host hello = %+v, want protocol 2", old)
			}
			client := claudehost.NewClient(f.Home)
			asking, working := uuid.NewString(), uuid.NewString()
			opened := f.Open(t, client, asking)
			f.Peer(t, opened.PID, test.prompt)
			request := listed(t, client, asking, 1).Requests[0]
			busy := f.Open(t, client, working)
			events := make(chan claudehost.Event, 256)
			subscribed, stop := context.WithCancel(ctx)
			defer stop()
			go func() { _, _ = client.Subscribe(subscribed, 0, func(e claudehost.Event) { events <- e }) }()
			if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: working, UUID: uuid.NewString(), Text: "SLOW 60000"}); err != nil {
				t.Fatal(err)
			}
			waitClaudeRunning(t, events, working)
			stop()
			retired := uuid.NewString()
			exiting := f.Open(t, client, retired)

			client, err := f.Manager(t, "1.1.0").Ensure(ctx)
			if err != nil {
				t.Fatalf("Ensure after the update = %v", err)
			}
			// The host retires the idle session as it drains, and its Claude
			// lingers after closing its output; a stop must still end it.
			testkit.Eventually(t, "the retired session's Claude to close its output and linger", 15*time.Second, func() bool {
				_, err := os.Stat(lingering + "." + strconv.Itoa(exiting.PID))
				return err == nil
			})
			if !claudehosttest.Alive(exiting.PID) {
				t.Fatalf("Claude %d exited before its retired session was stopped", exiting.PID)
			}
			if err := client.Close(ctx, retired); err != nil && !claudehost.IsCode(err, "not_open") {
				t.Fatalf("stopping a session the protocol 2 host retired = %v", err)
			}
			if claudehosttest.Alive(exiting.PID) {
				t.Fatalf("Claude %d still runs after its retired session was stopped", exiting.PID)
			}
			// The rest of the update needs its Claudes to exit when their input
			// closes, as Claude does.
			if err := os.WriteFile(lingering, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if hello, err := client.Hello(ctx); err != nil || hello.Protocol != claudehost.Protocol || hello.PID == old.PID {
				t.Fatalf("hello after the update = %+v, %v", hello, err)
			}
			if retiring := client.Retiring(); len(retiring) != 1 {
				t.Fatalf("retiring hosts = %v, want the protocol 2 host", retiring)
			}
			if got := listed(t, client, asking, 1); got.PID != opened.PID || got.Requests[0].RequestID != request.RequestID {
				t.Fatalf("listing after the update = %+v, want pid %d still asking %s", got, opened.PID, request.RequestID)
			}
			if resumed, err := client.Open(ctx, claudehost.OpenRequest{Resume: asking, Cwd: f.Cwd, Name: "kept"}); err != nil || resumed.PID != opened.PID {
				t.Fatalf("resume while the protocol 2 host runs it = %+v, %v; want pid %d", resumed, err, opened.PID)
			}
			if _, err := client.SetPermissionMode(ctx, asking, "plan"); err != nil {
				t.Fatalf("permission mode through the protocol 2 host = %v", err)
			}
			if result, err := client.Send(ctx, claudehost.SendRequest{SessionID: asking, UUID: uuid.NewString(), Text: "after the update", Origin: &claudehost.Origin{Kind: "human"}}); err != nil || !result.Accepted {
				t.Fatalf("send after the update = %+v, %v", result, err)
			}
			if state := listed(t, client, working, 0).State; state != "busy" {
				t.Fatalf("the session to stop is %q, want it busy", state)
			}
			closeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = client.Close(closeCtx, working)
			cancel()
			if err != nil {
				t.Fatalf("stopping a busy session through the protocol 2 host = %v", err)
			}
			if claudehosttest.Alive(busy.PID) {
				t.Fatalf("Claude %d still runs after its session was stopped", busy.PID)
			}
			if outcome, err := client.Respond(ctx, asking, request.RequestID, test.decision); err != nil || outcome != test.outcome {
				t.Fatalf("respond after the update = %q, %v; want %s", outcome, err, test.outcome)
			}
			testkit.Eventually(t, "the protocol 2 host to finish the turns and leave", 15*time.Second, func() bool {
				return !claudehosttest.Alive(old.PID) && !claudehosttest.Alive(opened.PID)
			})
			if got := replies(f.Turns(t, asking)); !slices.Equal(got, []string{test.reply, "echo:after the update"}) {
				t.Fatalf("turns = %v, want the answer to reach Claude and then the message", got)
			}
			controls, err := os.ReadFile(filepath.Join(f.ClaudeDir, "controls.jsonl"))
			if err != nil || !strings.Contains(string(controls), `"mode":"plan"`) || !strings.Contains(string(controls), asking) {
				t.Fatalf("controls = %s, %v; want the mode change at the protocol 2 host's Claude", controls, err)
			}

			resumed, err := client.Open(ctx, claudehost.OpenRequest{Resume: asking, Cwd: f.Cwd})
			if err != nil || resumed.PID == opened.PID {
				t.Fatalf("resume on this build's host = %+v, %v", resumed, err)
			}
			if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: asking, UUID: uuid.NewString(), Text: "on the new host"}); err != nil {
				t.Fatal(err)
			}
			testkit.Eventually(t, "the turn on the new host", 15*time.Second, func() bool { return len(f.Turns(t, asking)) == 3 })
		})
	}
}

// waitClaudeRunning waits for Claude to report that it runs a turn of the
// session; a host calls the session busy as soon as it passes a message on.
func waitClaudeRunning(t *testing.T, events <-chan claudehost.Event, sessionID string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-events:
			var message struct {
				Subtype string `json:"subtype"`
				State   string `json:"state"`
			}
			if e.SessionID == sessionID && e.Kind == "sdk" && json.Unmarshal(e.Message, &message) == nil && message.Subtype == "session_state_changed" && message.State == "running" {
				return
			}
		case <-deadline:
			t.Fatalf("Claude never reported running a turn of %s", sessionID)
		}
	}
}

// An idle-only close, on this build's host and on one that predates it,
// refuses a session with a turn running and leaves the turn to finish.
func TestCloseIdleRefusesAWorkingSessionAndLeavesItsTurnRunning(t *testing.T) {
	for _, protocol2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("protocol2=%v", protocol2), func(t *testing.T) {
			f := claudehosttest.New(t, "")
			ctx := testContext(t)
			client := claudehost.NewClient(f.Home)
			if protocol2 {
				f.StartProtocol2Host(t, "0.2.50")
			} else {
				var err error
				if client, err = f.Manager(t, "1.1.0").Ensure(ctx); err != nil {
					t.Fatal(err)
				}
			}
			sessionID := uuid.NewString()
			opened := f.Open(t, client, sessionID)
			if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: sessionID, UUID: uuid.NewString(), Text: "SLOW 1500"}); err != nil {
				t.Fatal(err)
			}
			if err := client.CloseIdle(ctx, sessionID); !claudehost.IsCode(err, "busy") {
				t.Fatalf("idle-only close of a working session = %v, want busy", err)
			}
			testkit.Eventually(t, "the turn to finish", 15*time.Second, func() bool { return slices.Equal(replies(f.Turns(t, sessionID)), []string{"slow:1500"}) })
			testkit.Eventually(t, "the session to go idle", 15*time.Second, func() bool { return listed(t, client, sessionID, 0).State == "idle" })
			if err := client.CloseIdle(ctx, sessionID); err != nil {
				t.Fatalf("idle-only close of an idle session = %v", err)
			}
			if claudehosttest.Alive(opened.PID) {
				t.Fatalf("Claude %d still runs after its idle session was closed", opened.PID)
			}
		})
	}
}
