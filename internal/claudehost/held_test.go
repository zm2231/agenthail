package claudehost_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
)

func stubHost(t *testing.T, socket string, answer func(method string) (any, *claudehost.Error)) {
	t.Helper()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var request struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
				}
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil || json.Unmarshal(line, &request) != nil {
					return
				}
				result, hostErr := answer(request.Method)
				reply := map[string]any{"id": request.ID}
				if hostErr != nil {
					reply["error"] = hostErr
				} else {
					reply["result"] = result
				}
				_ = json.NewEncoder(conn).Encode(reply)
			}()
		}
	}()
}

var (
	hello = claudehost.Hello{Protocol: claudehost.Protocol, CloseStops: true}
	held  = &claudehost.Error{Code: "held", Message: "Claude process 4242 from an earlier host still runs this session"}
)

// heldHosts stands up a current host that refuses every session as held by
// an earlier host's Claude until that Claude exits, and a retiring host that
// gives answer for the session, the Claude exiting as it does.
func heldHosts(t *testing.T, opened func(method string) (any, *claudehost.Error), answer func(method string) (any, *claudehost.Error)) claudehost.Client {
	t.Helper()
	home, err := os.MkdirTemp("", "ch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	client := claudehost.NewClient(home)
	if err := os.MkdirAll(filepath.Dir(client.Socket), 0o700); err != nil {
		t.Fatal(err)
	}
	var exited atomic.Bool
	stubHost(t, client.Socket, func(method string) (any, *claudehost.Error) {
		switch {
		case method == "hello":
			return hello, nil
		case method == "list":
			return claudehost.Listing{}, nil
		case !exited.Load():
			return nil, held
		}
		return opened(method)
	})
	stubHost(t, filepath.Join(filepath.Dir(client.Socket), "claude-host.4241.sock"), func(method string) (any, *claudehost.Error) {
		if method == "hello" {
			return hello, nil
		}
		result, err := answer(method)
		exited.Store(true)
		return result, err
	})
	return client
}

// A session a retiring host ran can end between the current host refusing it
// as held by that host's Claude and the retiring host being asked, which then
// no longer has it, or, holding it only as an earlier host's orphan, refuses
// it as held too. The session is no longer open anywhere, so the answer is
// not_open, as the current host now gives, not the stale held.
func TestASessionWhoseClaudeExitsWhileItIsReachedIsNotOpen(t *testing.T) {
	notOpen := func(string) (any, *claudehost.Error) {
		return nil, &claudehost.Error{Code: "not_open", Message: "not open"}
	}
	for _, retiring := range []struct {
		name   string
		answer func(string) (any, *claudehost.Error)
	}{
		{"not open there", notOpen},
		{"held there", func(string) (any, *claudehost.Error) { return nil, held }},
	} {
		for _, test := range []struct {
			name string
			call func(claudehost.Client, string) error
		}{
			{"close", func(c claudehost.Client, id string) error { return c.Close(testContext(t), id) }},
			{"send", func(c claudehost.Client, id string) error {
				_, err := c.Send(testContext(t), claudehost.SendRequest{SessionID: id, UUID: uuid.NewString(), Text: "x"})
				return err
			}},
		} {
			t.Run(test.name+" "+retiring.name, func(t *testing.T) {
				client := heldHosts(t, notOpen, retiring.answer)
				if err := test.call(client, uuid.NewString()); !claudehost.IsCode(err, "not_open") {
					t.Fatalf("%s = %v, want not_open", test.name, err)
				}
			})
		}
	}
}

// A resume refused as held while the retiring host that ran the conversation
// lets it go opens it on the current host.
func TestAResumeRefusedAsHeldWhileItsClaudeExitsOpensOnTheCurrentHost(t *testing.T) {
	sessionID := uuid.NewString()
	client := heldHosts(t, func(method string) (any, *claudehost.Error) {
		if method != "open" {
			return nil, &claudehost.Error{Code: "invalid", Message: method}
		}
		return claudehost.Session{SessionID: sessionID, PID: 4343}, nil
	}, func(string) (any, *claudehost.Error) { return claudehost.Listing{}, nil })
	opened, err := client.Open(testContext(t), claudehost.OpenRequest{Resume: sessionID, Cwd: "/"})
	if err != nil || opened.SessionID != sessionID || opened.PID != 4343 {
		t.Fatalf("resume = %+v, %v; want it opened on the current host", opened, err)
	}
}
