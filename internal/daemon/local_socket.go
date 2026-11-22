package daemon

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/daemonctl"
	"github.com/zm2231/agenthail/internal/peerproc"
)

// localAncestors bounds the walk up a caller's process tree; a real tree is
// far shallower, and the bound only ends a walk that never reaches init.
const localAncestors = 256

type hostedProcesses interface {
	HostedProcessSession(ctx context.Context, pid int) string
}

// unnamedSession stands for a session process whose record cannot be read: an
// agent runs there, though Agenthail cannot say which.
const unnamedSession = "\x00unnamed"

// startLocalSocket serves what only the person may do. A dashboard token is
// readable by every process the person runs, agents included, so a write
// here is told apart by the process that makes it, which the kernel names.
func (d *Daemon) startLocalSocket() (func(), error) {
	path := daemonctl.LocalSocketPath()
	listener, err := listenUserSocket(path)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agents", d.localAgentRolesHandler)
	server := &http.Server{
		Handler:           mux,
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
			d.log.Printf("local socket: %s", err)
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

// agentProcessSession names the session pid runs inside: the process itself
// or any ancestor is a session's Claude process, a Codex process holding a
// thread, or a Claude process a Claude host started. A live Claude process
// whose session record cannot be read is a session too, named unnamedSession.
func (d *Daemon) agentProcessSession(ctx context.Context, pid int) string {
	owns := func(pid int) string {
		if id := d.claudeProcessSession(pid); id != "" {
			return id
		}
		if threads := d.processThreads(pid); len(threads) > 0 {
			return threads[0]
		}
		for _, adapter := range d.Surfaces {
			if hosted, ok := adapter.(hostedProcesses); ok {
				if id := hosted.HostedProcessSession(ctx, pid); id != "" {
					return id
				}
			}
		}
		if _, found := d.unreadableRecord(pid); found {
			return unnamedSession
		}
		return ""
	}
	if id := owns(pid); id != "" {
		return id
	}
	return ancestorSession(pid, localAncestors, owns)
}

func (d *Daemon) localAgentRolesHandler(w http.ResponseWriter, r *http.Request) {
	peer, ok := r.Context().Value(mcpPeerKey{}).(peerproc.Peer)
	if !ok || peer.PID == 0 || peer.UID != os.Getuid() {
		writeAPIError(w, http.StatusForbidden, "person_only", "Agenthail could not name the process calling it.")
		return
	}
	if r.Method == http.MethodPost {
		if session := d.agentProcessSession(r.Context(), peer.PID); session == unnamedSession {
			d.tellPerson(r.Context(), "Agent role change refused", "A command run inside an agent session Agenthail could not name tried to change agent roles.", "", "agent.role.refused")
			writeAPIError(w, http.StatusForbidden, "person_only", "Agent roles are the person's to change, and this command runs inside an agent session Agenthail could not name: a Claude process among its parents has a session record that cannot be read. Run it from a terminal of your own.")
			return
		} else if session != "" {
			d.tellPerson(r.Context(), "Agent role change refused", "A command run inside session "+d.resolveDisplay(session)+" tried to change agent roles.", session, "agent.role.refused")
			writeAPIError(w, http.StatusForbidden, "person_only", "Agent roles are the person's to change, and this command runs inside session "+session+". Run it from a terminal of your own.")
			return
		}
	}
	d.serveAgentRoles(w, r, daemonapi.CLIActor)
}
