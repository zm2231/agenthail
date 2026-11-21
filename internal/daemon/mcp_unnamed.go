package daemon

import (
	"context"
	"errors"
	"sync"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// An empty tool list would stop the relay asking; an error keeps it asking.
func (d *Daemon) newUnnamedMCPServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "agenthail", Version: d.build.Version}, &mcp.ServerOptions{
		SupportedProtocolVersions: mcpProtocolVersions,
		Capabilities:              &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method != "tools/list" && method != "tools/call" {
				return next(ctx, method, request)
			}
			answer, _ := ctx.Value(mcpAnswerKey{}).(socketAnswer)
			return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest, Message: "Agenthail cannot tell which session is calling: " + answer.unnamed}
		}
	})
	return server
}

const unnamedCallersKept = 256

type unnamedCallers struct {
	mu   sync.Mutex
	last map[int]string
}

func (u *unnamedCallers) changed(pid int, why string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.last[pid] == why {
		return false
	}
	if u.last == nil {
		u.last = map[int]string{}
	}
	if len(u.last) >= unnamedCallersKept {
		for known := range u.last {
			if errors.Is(syscall.Kill(known, 0), syscall.ESRCH) {
				delete(u.last, known)
			}
		}
	}
	if len(u.last) < unnamedCallersKept {
		u.last[pid] = why
	}
	return true
}

func (d *Daemon) unnamedCaller(pid int, why string) socketAnswer {
	if d.unnamedCallers.changed(pid, why) {
		d.log.Printf("mcp auth session=- path=socket outcome=refused:unnamed pid=%d err=%s", pid, why)
	}
	return socketAnswer{unnamed: why}
}
