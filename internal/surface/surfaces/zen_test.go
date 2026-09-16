package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestZenSendForwardsSourceSessionIDToControlSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "zen-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "zen.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requestReceived := make(chan map[string]any, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		var request map[string]any
		if decodeErr := json.NewDecoder(bufio.NewReader(connection)).Decode(&request); decodeErr != nil {
			return
		}
		requestReceived <- request
		_, _ = connection.Write([]byte(`{"ok":true}` + "\n"))
	}()
	ctx, cancel := context.WithTimeout(surface.WithSourceSessionID(context.Background(), "source-session"), time.Second)
	defer cancel()
	result, err := NewZen().Send(ctx, &surface.Session{
		ID:        "target-session",
		Transport: "unix://" + socket + "?controller=zen&generation=1",
	}, "delegate")
	if err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	select {
	case request := <-requestReceived:
		if request["sourceSessionId"] != "source-session" {
			t.Fatalf("request=%+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("control socket did not receive a request")
	}
}
