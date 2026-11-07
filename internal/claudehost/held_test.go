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

// A session a retiring host ran can end between the current host refusing it
// as held by that host's Claude and the retiring host being asked, which then
// no longer has it. The session is no longer open anywhere, so the answer is
// not_open, as the current host now gives, not the stale held.
func TestASessionWhoseClaudeExitsWhileItIsReachedIsNotOpen(t *testing.T) {
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
		t.Run(test.name, func(t *testing.T) {
			home, err := os.MkdirTemp("", "ch")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(home) })
			client := claudehost.NewClient(home)
			if err := os.MkdirAll(filepath.Dir(client.Socket), 0o700); err != nil {
				t.Fatal(err)
			}
			hello := claudehost.Hello{Protocol: claudehost.Protocol, CloseStops: true}
			var exited atomic.Bool
			stubHost(t, client.Socket, func(method string) (any, *claudehost.Error) {
				if method == "hello" {
					return hello, nil
				}
				if !exited.Load() {
					return nil, &claudehost.Error{Code: "held", Message: "Claude process 4242 from an earlier host still runs this session"}
				}
				return nil, &claudehost.Error{Code: "not_open", Message: "not open"}
			})
			stubHost(t, filepath.Join(filepath.Dir(client.Socket), "claude-host.4241.sock"), func(method string) (any, *claudehost.Error) {
				if method == "hello" {
					return hello, nil
				}
				// This host forgets the session once its Claude has exited.
				exited.Store(true)
				return nil, &claudehost.Error{Code: "not_open", Message: "not open"}
			})
			if err := test.call(client, uuid.NewString()); !claudehost.IsCode(err, "not_open") {
				t.Fatalf("%s = %v, want not_open", test.name, err)
			}
		})
	}
}
