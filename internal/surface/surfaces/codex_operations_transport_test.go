package surfaces

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexOperationsUseNativeSocketAndReturnedIdentity(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "ah-ops-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", home)
	dir := filepath.Join(home, "app-server-control")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(dir, "app-server-control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan map[string]any, 40)
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request map[string]any
			if conn.ReadJSON(&request) != nil {
				return
			}
			id, ok := request["id"]
			if !ok {
				continue
			}
			requests <- request
			result := map[string]any{}
			switch request["method"] {
			case "thread/resume":
				result["thread"] = map[string]any{"canAcceptDirectInput": true, "status": "busy"}
			case "thread/fork":
				result["thread"] = map[string]any{"id": "fork-id", "cwd": "/fork", "source": "appServer", "name": "fork"}
			default:
				result["data"] = []any{}
				result["nextCursor"] = "page-two"
			}
			if conn.WriteJSON(map[string]any{"id": id, "result": result}) != nil {
				return
			}
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	codex := NewCodex("")
	codex.managed = false // An existing fixture socket needs no daemon lifecycle.
	session := &surface.Session{ID: "source", Surface: surface.KindCodex, Transport: codexTransportManaged, Source: "agenthail", Cwd: "/source"}
	fork, err := codex.ForkSession(context.Background(), session, surface.ForkOptions{BeforeTurnID: "cutoff"})
	if err != nil || fork.ID != "fork-id" || fork.Cwd != "/fork" || fork.Source != "appServer" || fork.Transport != codexTransportManaged {
		t.Fatalf("fork=%+v err=%v", fork, err)
	}
	for _, action := range []string{"list", "add", "update", "delete", "reorder", "start"} {
		result, err := codex.NativeQueue(context.Background(), session, surface.NativeQueueRequest{Action: action, Message: "next", ID: "q1", ClientID: "stable", IDs: []string{"q1"}, Cursor: "page-one"})
		if err != nil || result["nextCursor"] != "page-two" {
			t.Fatal(action, result, err)
		}
	}
	seen := map[string]map[string]any{}
	for len(requests) > 0 {
		request := <-requests
		seen[request["method"].(string)] = request["params"].(map[string]any)
	}
	if seen["thread/fork"]["beforeTurnId"] != "cutoff" || seen["thread/fork"]["deferGoalContinuation"] != true || seen["thread/fork"]["threadSource"] != "agenthail" {
		t.Fatal(seen)
	}
	if seen["thread/queue/add"]["clientUserMessageId"] != "stable" || seen["thread/queue/list"]["cursor"] != "page-one" {
		t.Fatal(seen)
	}
	session.Transport = codexTransportReadOnly
	if _, err := codex.NativeQueue(context.Background(), session, surface.NativeQueueRequest{Action: "start"}); !surface.IsDeliveryUnavailable(err) || surface.IsDeliveryOutcomeUnknown(err) {
		t.Fatal(err)
	}
}
