package claudesurface

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

// fakeHost answers each method with reply; a nil reply holds the call open
// without answering, as a host too slow for the caller's deadline does.
type fakeHost struct {
	mu    sync.Mutex
	reply map[string]func() any
	asked map[string]int
}

func (f *fakeHost) set(method string, reply func() any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply[method] = reply
}

func fakeHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "ah-silent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if err := os.MkdirAll(filepath.Dir(claudehost.SocketPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func startFakeHost(t *testing.T) (string, *fakeHost) {
	t.Helper()
	home := fakeHome(t)
	return home, serveFakeHost(t, claudehost.SocketPath(home))
}

func serveFakeHost(t *testing.T, socket string) *fakeHost {
	t.Helper()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	host := &fakeHost{reply: map[string]func() any{}, asked: map[string]int{}}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var request struct {
					Method string `json:"method"`
				}
				_ = json.Unmarshal(line, &request)
				host.mu.Lock()
				host.asked[request.Method]++
				reply := host.reply[request.Method]
				host.mu.Unlock()
				var answer any
				if reply != nil {
					answer = reply()
				}
				if answer == nil {
					_, _ = conn.Read(make([]byte, 1))
					return
				}
				out, _ := json.Marshal(answer)
				_, _ = conn.Write(append(out, '\n'))
			}()
		}
	}()
	return host
}

func (f *fakeHost) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[method]
}

func hostResult(result any) func() any {
	return func() any { return map[string]any{"id": 1, "result": result} }
}

func hostError(code, message string) func() any {
	return func() any {
		return map[string]any{"id": 1, "error": map[string]string{"code": code, "message": message}}
	}
}

func TestARequestIsAnsweredWhileTheListingOfEveryHostIsTooSlow(t *testing.T) {
	home, host := startFakeHost(t)
	session := surface.Session{ID: "slow-listing", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid()}
	asked := claudehost.Session{SessionID: session.ID, PID: session.PID, PermissionMode: "default", Requests: []claudehost.Request{{RequestID: "r1", Kind: "question", CreatedAt: time.Now()}}}
	host.set("list", hostResult(claudehost.Listing{Sessions: []claudehost.Session{asked}}))
	claude := NewClaude(nil, home)
	ctx := context.Background()
	if waiting, known := claude.hostWaiting(ctx, &session); !known || waiting == nil || waiting.ID != "r1" {
		t.Fatalf("waiting while the host lists = %+v (known %v); want r1", waiting, known)
	}
	host.set("list", nil)
	claude.host.forget()
	listing := make(chan struct{})
	go func() {
		defer close(listing)
		claude.host.listing(ctx)
	}()
	testkitWait(t, func() bool { return host.count("list") == 2 })
	held := make(chan struct{})
	host.set("session", func() any {
		<-held
		return map[string]any{"id": 1, "result": asked}
	})
	host.set("respond", hostResult(map[string]string{"outcome": "answered"}))
	read := make(chan error, 1)
	readStarted := time.Now()
	go func() {
		state, err := claude.Requests(ctx, &session)
		if err == nil && (len(state.Requests) != 1 || state.Requests[0].ID != "r1") {
			err = errors.New("the held read did not list r1")
		}
		read <- err
	}()
	testkitWait(t, func() bool { return host.count("session") == 1 })
	if outcome, err := claude.Respond(ctx, &session, "r1", surface.Decision{Behavior: "allow"}); err != nil || outcome != "answered" {
		t.Fatalf("respond while the listing hangs = %q, %v; want answered", outcome, err)
	}
	close(held)
	if err := <-read; err != nil {
		t.Fatalf("requests while the listing hangs: %v", err)
	}
	if took := time.Since(readStarted); took >= claudeHostListTimeout {
		t.Fatalf("the read and the answer took %s, waiting on the listing", took)
	}
	select {
	case <-listing:
		t.Fatal("the listing ended before the answer; the check proves nothing")
	default:
	}
	<-listing
	claude.host.mu.Lock()
	cached, ok := claude.host.open[session.ID]
	claude.host.mu.Unlock()
	if !ok || len(cached.Requests) != 0 {
		t.Fatalf("cached session after the answer = %+v (listed %v); want it kept without r1", cached, ok)
	}
}

func TestASessionReadStoredDuringAListingIsNotReplacedByIt(t *testing.T) {
	home, host := startFakeHost(t)
	session := surface.Session{ID: "read-during-listing", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid()}
	quiet := claudehost.Session{SessionID: session.ID, PID: session.PID}
	asking := quiet
	asking.Requests = []claudehost.Request{{RequestID: "r1", Kind: "question", CreatedAt: time.Now()}}
	host.set("list", hostResult(claudehost.Listing{Sessions: []claudehost.Session{quiet}}))
	claude := NewClaude(nil, home)
	ctx := context.Background()
	if waiting, known := claude.hostWaiting(ctx, &session); !known || waiting != nil {
		t.Fatalf("waiting before the request = %+v (known %v); want none", waiting, known)
	}
	readHeld, listHeld := make(chan struct{}), make(chan struct{})
	host.set("session", func() any {
		<-readHeld
		return map[string]any{"id": 1, "result": asking}
	})
	read := make(chan error, 1)
	go func() {
		_, err := claude.Requests(ctx, &session)
		read <- err
	}()
	testkitWait(t, func() bool { return host.count("session") == 1 })
	host.set("list", func() any {
		<-listHeld
		return map[string]any{"id": 1, "result": claudehost.Listing{Sessions: []claudehost.Session{quiet}}}
	})
	claude.host.forget()
	listing := make(chan struct{})
	go func() {
		defer close(listing)
		claude.host.listing(ctx)
	}()
	testkitWait(t, func() bool { return host.count("list") == 2 })
	close(readHeld)
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	close(listHeld)
	<-listing
	if waiting, known := claude.hostWaiting(ctx, &session); !known || waiting == nil || waiting.ID != "r1" {
		t.Fatalf("waiting after a listing that started before the read was stored = %+v (known %v); want r1", waiting, known)
	}
}

func TestAnOlderHostThatDoesNotRunTheSessionCannotAnswerForIt(t *testing.T) {
	home := fakeHome(t)
	socket := claudehost.SocketPath(home)
	current := serveFakeHost(t, socket)
	older := serveFakeHost(t, strings.TrimSuffix(socket, ".sock")+"."+strconv.Itoa(os.Getpid())+".sock")
	owner := serveFakeHost(t, strings.TrimSuffix(socket, ".sock")+"."+strconv.Itoa(os.Getppid())+".sock")
	session := surface.Session{ID: "newer-owner", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid()}
	current.set("session", hostError("not_open", "not open"))
	current.set("sessionCommands", hostError("not_open", "not open"))
	older.set("session", hostError("invalid", "unknown method: session"))
	older.set("sessionCommands", hostError("invalid", "unknown method: sessionCommands"))
	older.set("list", hostResult(claudehost.Listing{Draining: true}))
	later := func(reply func() any) func() any {
		return func() any {
			time.Sleep(100 * time.Millisecond)
			return reply()
		}
	}
	owner.set("session", later(hostResult(claudehost.Session{SessionID: session.ID, PID: session.PID, Requests: []claudehost.Request{{RequestID: "r1", Kind: "question", CreatedAt: time.Now()}}})))
	owner.set("sessionCommands", later(hostResult(map[string][]claudehost.Command{"commands": {{Name: "owned"}}})))
	claude := NewClaude(nil, home)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if state, err := claude.Requests(ctx, &session); err != nil || len(state.Requests) != 1 || state.Requests[0].ID != "r1" {
		t.Fatalf("requests = %+v, %v; want the owner's r1", state, err)
	}
	if commands, err := claude.Commands(ctx, &session); err != nil || len(commands.Commands) != 1 || commands.Commands[0].Insert != "/owned" {
		t.Fatalf("commands = %+v, %v; want the owner's /owned", commands, err)
	}
}

func testkitWait(t *testing.T, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

func TestARequestOfASessionNoHostRunsIsNone(t *testing.T) {
	home, host := startFakeHost(t)
	host.set("session", hostError("not_open", "Claude session gone is not open in the host"))
	session := surface.Session{ID: "gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid()}
	if state, err := NewClaude(nil, home).Requests(context.Background(), &session); err != nil || len(state.Requests) != 0 {
		t.Fatalf("requests = %+v, %v; want none", state, err)
	}
}

func TestARequestOnAnOlderRetiringHostIsAnsweredWithoutListingEveryHost(t *testing.T) {
	home := fakeHome(t)
	socket := claudehost.SocketPath(home)
	current := serveFakeHost(t, socket)
	older := serveFakeHost(t, strings.TrimSuffix(socket, ".sock")+"."+strconv.Itoa(os.Getpid())+".sock")
	silent := serveFakeHost(t, strings.TrimSuffix(socket, ".sock")+"."+strconv.Itoa(os.Getppid())+".sock")
	session := surface.Session{ID: "older-session", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid()}
	notOpen := hostError("not_open", "Claude session older-session is not open in the host")
	for _, host := range []*fakeHost{current, silent} {
		for _, method := range []string{"session", "sessionCommands", "respond"} {
			host.set(method, notOpen)
		}
	}
	var open atomic.Bool
	open.Store(true)
	older.set("session", hostError("invalid", "unknown method: session"))
	older.set("sessionCommands", hostError("invalid", "unknown method: sessionCommands"))
	older.set("list", func() any {
		listed := claudehost.Session{SessionID: session.ID, PID: session.PID, PermissionMode: "default", Commands: []claudehost.Command{{Name: "review"}}}
		if open.Load() {
			listed.Requests = []claudehost.Request{{RequestID: "r1", Kind: "question", CreatedAt: time.Now()}}
		}
		return map[string]any{"id": 1, "result": claudehost.Listing{Sessions: []claudehost.Session{listed}, Draining: true}}
	})
	older.set("respond", func() any {
		open.Store(false)
		return map[string]any{"id": 1, "result": map[string]string{"outcome": "answered"}}
	})
	claude := NewClaude(nil, home)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	state, err := claude.Requests(ctx, &session)
	if err != nil || len(state.Requests) != 1 || state.Requests[0].ID != "r1" {
		t.Fatalf("requests on the older host = %+v, %v; want r1", state, err)
	}
	if outcome, err := claude.Respond(ctx, &session, "r1", surface.Decision{Behavior: "allow"}); err != nil || outcome != "answered" {
		t.Fatalf("respond = %q, %v; want answered", outcome, err)
	}
	if state, err := claude.Requests(ctx, &session); err != nil || len(state.Requests) != 0 {
		t.Fatalf("requests after the answer = %+v, %v; want none", state, err)
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("answering took %s; it waited on a host that does not run the session", took)
	}
	if current.count("list") != 0 || silent.count("list") != 0 {
		t.Fatalf("listings asked of hosts that do not run the session: current %d, other retiring %d", current.count("list"), silent.count("list"))
	}
	if commands, err := claude.Commands(ctx, &session); err != nil || len(commands.Commands) != 1 || commands.Commands[0].Insert != "/review" {
		t.Fatalf("commands of the older host's session = %+v, %v; want /review", commands, err)
	}
}

func TestAnOlderHostOffersTheCommandsItListsWithItsSessions(t *testing.T) {
	home, host := startFakeHost(t)
	session := surface.Session{ID: "older", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid()}
	host.set("sessionCommands", hostError("invalid", "unknown method: sessionCommands"))
	host.set("list", hostResult(claudehost.Listing{Sessions: []claudehost.Session{{SessionID: session.ID, PID: session.PID, Commands: []claudehost.Command{{Name: "review"}}}}}))
	commands, err := NewClaude(nil, home).Commands(context.Background(), &session)
	if err != nil || len(commands.Commands) != 1 || commands.Commands[0].Insert != "/review" {
		t.Fatalf("commands = %+v, %v; want /review", commands, err)
	}
}
