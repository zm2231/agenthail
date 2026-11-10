package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestARequestListedByAHostThatWentSilentIsServedOnlyBriefly(t *testing.T) {
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
	session := surface.Session{ID: "silent-session", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, PID: os.Getpid()}
	listed := claudehost.Listing{Sessions: []claudehost.Session{{SessionID: session.ID, PID: session.PID, Requests: []claudehost.Request{{RequestID: "r1", Kind: "permission", CreatedAt: time.Now()}}}}}
	var answered atomic.Bool
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
					return
				}
				if answered.Swap(true) {
					_, _ = conn.Read(make([]byte, 1))
					return
				}
				result, _ := json.Marshal(listed)
				reply, _ := json.Marshal(map[string]any{"id": 1, "result": json.RawMessage(result)})
				_, _ = conn.Write(append(reply, '\n'))
			}()
		}
	}()
	claude := NewClaude(nil, home)
	ctx := context.Background()
	requests := func() ([]surface.Request, error) {
		state, err := claude.Requests(ctx, &session)
		return state.Requests, err
	}
	if got, err := requests(); err != nil || len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("requests while the host answers = %+v, %v; want r1", got, err)
	}
	if got, err := requests(); err != nil || len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("requests just after the host went silent = %+v, %v; want r1", got, err)
	}
	time.Sleep(claudeHostSilentOpenTTL)
	if got, err := requests(); err == nil {
		t.Fatalf("requests long after the host went silent = %+v, want an error", got)
	}
}
