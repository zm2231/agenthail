package testkit

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
)

type dialGuard struct {
	addr   string
	mu     sync.Mutex
	stacks []string
}

func (g *dialGuard) record() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stacks = append(g.stacks, string(debug.Stack()))
}

func (g *dialGuard) attempts() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.stacks...)
}

// One wrapper serves every guard, so guards can be restored in any order.
type dialGuardSet struct {
	mu        sync.Mutex
	installed bool
	active    map[*dialGuard]bool
}

var dialGuards dialGuardSet

func (s *dialGuardSet) add(guard *dialGuard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.installed {
		transport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			panic("http.DefaultTransport is not an *http.Transport")
		}
		next := transport.DialContext
		transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
			if s.refuse(target) {
				return nil, fmt.Errorf("a test dialed %s, the port a person's own Agenthail listens on", target)
			}
			return next(ctx, network, target)
		}
		s.installed = true
		s.active = map[*dialGuard]bool{}
	}
	s.active[guard] = true
}

func (s *dialGuardSet) remove(guard *dialGuard) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, guard)
}

func (s *dialGuardSet) refuse(target string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	refused := false
	for guard := range s.active {
		if guard.addr == target {
			guard.record()
			refused = true
		}
	}
	return refused
}
