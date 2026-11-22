package mcpstdio

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const daemonTools = 200

func fakeDaemon(t *testing.T, ready *atomic.Bool) string {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "daemon", Version: "test"}, &mcp.ServerOptions{SupportedProtocolVersions: []string{"2026-07-28", "2025-11-25", "2025-06-18"}})
	for i := range daemonTools {
		mcp.AddTool(server, &mcp.Tool{Name: fmt.Sprintf("tool_%03d", i)}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})
	}
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "tools/list" && !ready.Load() {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest, Message: "not yet"}
			}
			return next(ctx, method, request)
		}
	})
	dir, err := os.MkdirTemp("/tmp", "ahr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "mcp.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})}
	go httpServer.Serve(listener)
	t.Cleanup(func() { httpServer.Close() })
	return socket
}

func TestASessionNeverSeesPartOfTheToolsTheRelayAddsLate(t *testing.T) {
	defer func(wait time.Duration) { startWait = wait }(startWait)
	startWait = 50 * time.Millisecond
	var ready atomic.Bool
	socket := fakeDaemon(t, &ready)
	clientIn, relayOut := io.Pipe()
	relayIn, clientOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go Run(ctx, socket, "test", relayIn, relayOut)
	changed := make(chan struct{}, 1)
	client := mcp.NewClient(&mcp.Implementation{Name: "session", Version: "test"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			select {
			case changed <- struct{}{}:
			default:
			}
		},
	})
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: clientIn, Writer: clientOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	ready.Store(true)
	deadline := time.Now().Add(15 * time.Second)
	for {
		listed, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(listed.Tools); n != 0 && n != daemonTools {
			t.Fatalf("the session saw %d of the %d tools while the relay added them", n, daemonTools)
		}
		if len(listed.Tools) == daemonTools {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the relay never listed the daemon's tools")
		}
	}
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("the session was never told its tools changed")
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: fmt.Sprintf("tool_%03d", daemonTools-1)})
	if err != nil || result.IsError {
		t.Fatalf("calling the last tool after the change: %v %+v", err, result)
	}
}
