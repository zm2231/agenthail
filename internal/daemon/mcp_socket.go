package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/peerproc"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const mcpSocketAncestors = 4

var (
	mcpSocketSessionWait = 5 * time.Second
	mcpSocketShutdown    = 5 * time.Second
)

func MCPSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "mcp.sock")
}

type mcpPeerKey struct{}

type processSessions interface {
	ProcessSession(pid int) string
}

type processThreads interface {
	ProcessThreads(pid int) []string
}

// socketAnswer is what the socket serves one request: a caller's tools, the
// tools with every call refused for the reason given, or no tools at all.
type socketAnswer struct {
	caller  *mcpCaller
	tools   bool
	refusal *mcpRefusal
	status  int
}

type mcpAnswerKey struct{}

func mcpNoCallerRefusal(ctx context.Context) *mcpRefusal {
	if answer, ok := ctx.Value(mcpAnswerKey{}).(socketAnswer); ok && answer.refusal != nil {
		return answer.refusal
	}
	return &mcpRefusal{Code: "unauthenticated", Message: "This session is still starting; try again in a moment.", Retryable: true}
}

func (d *Daemon) startMCPSocket() (func(), error) {
	path := MCPSocketPath()
	listener, err := listenUserSocket(path)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Handler:           d.mcpSocketHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if peer, err := peerproc.Of(conn); err == nil {
				return context.WithValue(ctx, mcpPeerKey{}, peer)
			}
			return ctx
		},
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			d.log.Printf("MCP socket: %s", err)
		}
	}()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), mcpSocketShutdown)
		defer cancel()
		if err := server.Shutdown(ctx); errors.Is(err, context.DeadlineExceeded) {
			_ = server.Close()
		}
		_ = os.Remove(path)
	}, nil
}

// mcpSocketHandler serves a process the tools only when the kernel names a
// live session process among its ancestors, or a Codex process as its parent;
// any other process gets a server with no tools.
func (d *Daemon) mcpSocketHandler() http.Handler {
	empty, full := d.newBareMCPServer(), d.newMCPServer()
	forCaller := d.mcpServerFor(full)
	stream := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		answer, _ := r.Context().Value(mcpAnswerKey{}).(socketAnswer)
		switch {
		case answer.caller != nil:
			return forCaller(r)
		case !answer.tools:
			return empty
		}
		if server := d.extensionToolServer(); server != nil {
			return server
		}
		return full
	}, mcpStreamOptions)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, mcp.DefaultMaxRequestBodyBytes)
		}
		body, err := readMCPRequest(r)
		if errors.As(err, new(*http.MaxBytesError)) {
			http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		peer, _ := r.Context().Value(mcpPeerKey{}).(peerproc.Peer)
		request := mcpSocketRequest{peer: peer, post: r.Method == http.MethodPost, body: body, bodyErr: err}
		answer, err := withinBound(d, r.Context(), "naming the calling session", mcpCallerBound, func(ctx context.Context) (socketAnswer, error) {
			return d.socketCaller(ctx, request), nil
		})
		var stalled *mcpStallError
		switch {
		case errors.As(err, &stalled):
			d.logMCPAuthRefusal("socket", "-", "busy", err)
			answer = socketAnswer{tools: true, refusal: stalled.refusal()}
		case err != nil:
			d.logMCPAuthRefusal("socket", "-", "busy", err)
			answer = socketAnswer{tools: true, refusal: mcpCheckFailed()}
		}
		if answer.status != 0 {
			http.Error(w, http.StatusText(answer.status), answer.status)
			return
		}
		ctx := context.WithValue(r.Context(), mcpAnswerKey{}, answer)
		if answer.caller != nil {
			ctx = context.WithValue(ctx, mcpCallerKey{}, answer.caller)
		}
		stream.ServeHTTP(w, r.WithContext(ctx))
	})
}

// mcpSocketRequest is what naming a socket request's caller reads from it,
// taken before the bounded work so that work never touches the request.
type mcpSocketRequest struct {
	peer    peerproc.Peer
	post    bool
	body    []byte
	bodyErr error
}

// readMCPRequest reads a POST body once and puts it back for the MCP handler.
func readMCPRequest(r *http.Request) ([]byte, error) {
	if r.Method != http.MethodPost || r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, err
}

func (d *Daemon) socketCaller(ctx context.Context, request mcpSocketRequest) socketAnswer {
	peer := request.peer
	if peer.PID == 0 || peer.UID != os.Getuid() {
		return socketAnswer{}
	}
	if threads := d.codexParentThreads(peer.PID); len(threads) > 0 {
		return d.codexCaller(ctx, request, threads)
	}
	sessionID := d.processSession(peer.PID)
	if sessionID == "" {
		return socketAnswer{}
	}
	record, err := d.awaitLocalSession(ctx, sessionID)
	if sessionLookupFailed(err) {
		d.logMCPAuthRefusal("socket", sessionID, "busy", err)
		return socketAnswer{tools: true, refusal: mcpCheckFailed()}
	}
	if err != nil {
		return socketAnswer{}
	}
	caller, err := d.agentCaller(record)
	if err != nil {
		d.logMCPAuthRefusal("socket", sessionID, "busy", err)
		return socketAnswer{tools: true, refusal: mcpCheckFailed()}
	}
	if d.projectToolsOff(caller.project) {
		return socketAnswer{}
	}
	return socketAnswer{caller: caller, tools: true}
}

// codexParentThreads names the threads loaded by the peer's parent when that
// parent is a Codex process. Codex starts its stdio servers as its direct
// children, and a command its model runs sits below a shell or a sandbox.
func (d *Daemon) codexParentThreads(pid int) []string {
	parent, err := peerproc.Parent(pid)
	if err != nil || parent <= 1 {
		return nil
	}
	for _, adapter := range d.Surfaces {
		if owner, ok := adapter.(processThreads); ok {
			if threads := owner.ProcessThreads(parent); len(threads) > 0 {
				return threads
			}
		}
	}
	return nil
}

// codexCaller names the caller of a Codex process's stdio server. One server
// serves several threads, and only a tool call names its thread, so the tools
// are listed to any Codex child and each call acts as the thread it names,
// once the Codex process is shown to hold that thread.
func (d *Daemon) codexCaller(ctx context.Context, request mcpSocketRequest, loaded []string) socketAnswer {
	method, thread, err := mcpRequestThread(request)
	refused := func(code, message string) socketAnswer {
		if code == "unauthenticated" {
			d.logMCPAuthRefusal("socket", thread, code, nil)
		}
		return socketAnswer{tools: true, refusal: &mcpRefusal{Code: code, Message: message}}
	}
	if err != nil {
		return refused("invalid_request", "A Codex server's request must be one JSON-RPC message.")
	}
	if method != "tools/call" {
		return socketAnswer{tools: true}
	}
	if thread == "" {
		return refused("unauthenticated", "This Codex process named no thread for the call, so Agenthail cannot tell which session is calling.")
	}
	if !slices.Contains(loaded, thread) {
		return refused("unauthenticated", "The Codex process that started this server has no thread "+thread+" loaded.")
	}
	record, err := d.awaitLocalSession(ctx, thread)
	if sessionLookupFailed(err) {
		d.logMCPAuthRefusal("socket", thread, "busy", err)
		return socketAnswer{tools: true, refusal: mcpCheckFailed()}
	}
	if err != nil || record.Session.Surface != surface.KindCodex {
		return refused("unauthenticated", "Codex thread "+thread+" is not a session on this host; it may have moved or been archived.")
	}
	caller, err := d.agentCaller(record)
	if err != nil {
		d.logMCPAuthRefusal("socket", thread, "busy", err)
		return socketAnswer{tools: true, refusal: mcpCheckFailed()}
	}
	if d.projectToolsOff(caller.project) {
		return refused("tools_off", "The Agenthail tools are off for this project's sessions that Agenthail did not start; agenthail project mcp <project> on turns them back on.")
	}
	return socketAnswer{caller: caller, tools: true}
}

// mcpRequestThread reads the JSON-RPC method and the thread Codex puts in a
// tool call's _meta. A body that is not one JSON object, such as a batch, is
// an error.
func mcpRequestThread(request mcpSocketRequest) (method, thread string, err error) {
	if !request.post {
		return "", "", nil
	}
	if request.bodyErr != nil {
		return "", "", request.bodyErr
	}
	var message struct {
		Method string `json:"method"`
		Params struct {
			Meta struct {
				ThreadID string `json:"threadId"`
			} `json:"_meta"`
		} `json:"params"`
	}
	if err := json.Unmarshal(request.body, &message); err != nil {
		return "", "", err
	}
	return message.Method, message.Params.Meta.ThreadID, nil
}

func (d *Daemon) processSession(pid int) string {
	for range mcpSocketAncestors {
		parent, err := peerproc.Parent(pid)
		if err != nil || parent <= 1 {
			return ""
		}
		pid = parent
		for _, adapter := range d.Surfaces {
			if owner, ok := adapter.(processSessions); ok {
				if id := owner.ProcessSession(pid); id != "" {
					return id
				}
			}
		}
	}
	return ""
}

// awaitLocalSession waits for discovery to register a session that has just
// started: Claude starts its MCP servers as soon as it writes its record, and
// lists their tools once.
func (d *Daemon) awaitLocalSession(ctx context.Context, sessionID string) (registry.CallerRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, mcpSocketSessionWait)
	defer cancel()
	for {
		record, err := d.Registry.Caller(sessionID)
		if err == nil {
			if record.HostID != "" || record.Archived {
				return registry.CallerRecord{}, errMCPSessionEnded
			}
			return record, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return registry.CallerRecord{}, err
		}
		select {
		case d.catalogNudge <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return registry.CallerRecord{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

var errMCPSessionEnded = errors.New("the session has moved or been archived")

// sessionLookupFailed is a registry failure, not a session that is missing or ended.
func sessionLookupFailed(err error) bool {
	return err != nil && !errors.Is(err, errMCPSessionEnded) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled)
}
