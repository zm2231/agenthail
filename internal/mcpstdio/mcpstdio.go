// Package mcpstdio serves a session the Agenthail tools over stdio, relaying
// every call to the daemon's MCP socket. The daemon names the caller from the
// socket's peer process, so nothing here carries a credential.
package mcpstdio

import (
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	firstRetry = 250 * time.Millisecond
	lastRetry  = 5 * time.Second
)

// startWait bounds how long the relay holds the session's handshake for the
// daemon: past the daemon's own wait for a new session's registration, and
// well inside the time Claude allows a server to start.
var startWait = 8 * time.Second

func Run(ctx context.Context, socket, version string, in io.ReadCloser, out io.WriteCloser) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "agenthail", Version: version}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
	})
	relay := &relay{server: server, client: mcp.NewClient(&mcp.Implementation{Name: "agenthail-stdio", Version: version}, nil), socket: socket}
	relayCtx, stop := context.WithCancel(ctx)
	defer stop()
	startCtx, started := context.WithTimeout(relayCtx, startWait)
	registered := relay.register(startCtx)
	started()
	session, err := server.Connect(ctx, &mcp.IOTransport{Reader: in, Writer: out}, nil)
	if err != nil {
		return err
	}
	if !registered {
		go relay.register(relayCtx)
	}
	return session.Wait()
}

type relay struct {
	server *mcp.Server
	client *mcp.Client
	socket string
}

// register adds the daemon's tools once it answers. Tools added after the
// session connected reach it through tools/list_changed.
func (r *relay) register(ctx context.Context) bool {
	transport := &mcp.StreamableClientTransport{Endpoint: "http://agenthail/mcp", HTTPClient: socketClient(r.socket), DisableStandaloneSSE: true}
	for wait := firstRetry; ; wait = min(wait*2, lastRetry) {
		if daemon, tools, err := r.list(ctx, transport); err == nil {
			for _, tool := range tools {
				r.server.AddTool(tool, forward(daemon, tool.Name))
			}
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
	}
}

func (r *relay) list(ctx context.Context, transport mcp.Transport) (*mcp.ClientSession, []*mcp.Tool, error) {
	daemon, err := r.client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, err
	}
	var tools []*mcp.Tool
	for tool, err := range daemon.Tools(ctx, nil) {
		if err != nil {
			_ = daemon.Close()
			return nil, nil, err
		}
		tools = append(tools, tool)
	}
	return daemon, tools, nil
}

func forward(daemon *mcp.ClientSession, name string) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return daemon.CallTool(ctx, &mcp.CallToolParams{Meta: request.Params.Meta, Name: name, Arguments: request.Params.Arguments})
	}
}

func socketClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
}
