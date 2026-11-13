package daemon

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/peerproc"
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
// live session process among its ancestors; any other process gets a server
// with no tools.
func (d *Daemon) mcpSocketHandler() http.Handler {
	full, empty := d.newMCPServer(), d.newMCPServer()
	d.registerMCPTools(full)
	stream := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if mcpCallerFrom(r.Context()) == nil {
			return empty
		}
		return full
	}, mcpStreamOptions)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if caller := d.socketCaller(r.Context()); caller != nil {
			r = r.WithContext(context.WithValue(r.Context(), mcpCallerKey{}, caller))
		}
		stream.ServeHTTP(w, r)
	})
}

func (d *Daemon) socketCaller(ctx context.Context) *mcpCaller {
	peer, ok := ctx.Value(mcpPeerKey{}).(peerproc.Peer)
	if !ok || peer.UID != os.Getuid() {
		return nil
	}
	sessionID := d.processSession(peer.PID)
	if sessionID == "" {
		return nil
	}
	session, err := d.awaitLocalSession(ctx, sessionID)
	if err != nil {
		return nil
	}
	caller, err := d.agentCaller(session)
	if err != nil {
		d.log.Printf("MCP socket: reading the permissions of %s failed: %s", d.resolveDisplay(sessionID), err)
		return nil
	}
	return caller
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
func (d *Daemon) awaitLocalSession(ctx context.Context, sessionID string) (surface.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, mcpSocketSessionWait)
	defer cancel()
	for {
		session, err := d.Registry.Session(sessionID)
		if err == nil {
			return d.servableSession(*session)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return surface.Session{}, err
		}
		select {
		case d.catalogNudge <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return surface.Session{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (d *Daemon) servableSession(session surface.Session) (surface.Session, error) {
	host, err := d.Registry.SessionHost(session.ID)
	if err != nil {
		return surface.Session{}, err
	}
	archived, err := d.Registry.SessionArchived(session.ID)
	if err != nil {
		return surface.Session{}, err
	}
	if host != "" || archived {
		return surface.Session{}, errMCPSessionEnded
	}
	return session, nil
}

var errMCPSessionEnded = errors.New("the session has moved or been archived")
