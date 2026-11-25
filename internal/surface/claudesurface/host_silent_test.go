package claudesurface

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
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
}

func (f *fakeHost) set(method string, reply func() any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply[method] = reply
}

func startFakeHost(t *testing.T) (string, *fakeHost) {
	t.Helper()
	home, err := os.MkdirTemp("", "ah-silent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	socket := claudehost.SocketPath(home)
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	host := &fakeHost{reply: map[string]func() any{}}
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
	return home, host
}

func hostResult(result any) func() any {
	return func() any { return map[string]any{"id": 1, "result": result} }
}

func hostError(code, message string) func() any {
	return func() any { return map[string]any{"id": 1, "error": map[string]string{"code": code, "message": message}} }
}

func TestARequestIsAnsweredWhileTheListingOfEveryHostIsTooSlow(t *testing.T) {
	home, host := startFakeHost(t)
	session := surface.Session{ID: "slow-listing", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid()}
	var open atomic.Bool
	open.Store(true)
	listed := func() any {
		listing := claudehost.Session{SessionID: session.ID, PID: session.PID, PermissionMode: "default"}
		if open.Load() {
			listing.Requests = []claudehost.Request{{RequestID: "r1", Kind: "question", CreatedAt: time.Now()}}
		}
		return map[string]any{"id": 1, "result": listing}
	}
	host.set("session", listed)
	host.set("respond", func() any {
		open.Store(false)
		return map[string]any{"id": 1, "result": map[string]string{"outcome": "answered"}}
	})
	claude := NewClaude(nil, home)
	ctx := context.Background()
	state, err := claude.Requests(ctx, &session)
	if err != nil || len(state.Requests) != 1 || state.Requests[0].ID != "r1" || state.PermissionMode != "default" {
		t.Fatalf("requests while the listing does not answer = %+v, %v; want r1", state, err)
	}
	if outcome, err := claude.Respond(ctx, &session, "r1", surface.Decision{Behavior: "allow"}); err != nil || outcome != "answered" {
		t.Fatalf("respond = %q, %v; want answered", outcome, err)
	}
	if waiting, known := claude.hostWaiting(ctx, &session); known && waiting != nil {
		t.Fatalf("waiting after the answer while the listing does not answer = %+v; want none", waiting)
	}
	if state, err := claude.Requests(ctx, &session); err != nil || len(state.Requests) != 0 {
		t.Fatalf("requests after the answer = %+v, %v; want none", state, err)
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

func TestARequestListedByAnOlderHostThatWentSilentIsServedOnlyBriefly(t *testing.T) {
	home, host := startFakeHost(t)
	session := surface.Session{ID: "silent-session", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid()}
	host.set("session", hostError("invalid", "unknown method: session"))
	host.set("list", hostResult(claudehost.Listing{Sessions: []claudehost.Session{{SessionID: session.ID, PID: session.PID, Requests: []claudehost.Request{{RequestID: "r1", Kind: "permission", CreatedAt: time.Now()}}}}}))
	claude := NewClaude(nil, home)
	ctx := context.Background()
	requests := func() ([]surface.Request, error) {
		state, err := claude.Requests(ctx, &session)
		return state.Requests, err
	}
	if got, err := requests(); err != nil || len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("requests while the host answers = %+v, %v; want r1", got, err)
	}
	host.set("list", nil)
	if got, err := requests(); err != nil || len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("requests just after the host went silent = %+v, %v; want r1", got, err)
	}
	time.Sleep(claudeHostSilentOpenTTL)
	if got, err := requests(); err == nil {
		t.Fatalf("requests long after the host went silent = %+v, want an error", got)
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
