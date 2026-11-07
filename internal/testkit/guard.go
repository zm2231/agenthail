package testkit

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"sync"
)

// GuardDialsTo refuses HTTP dials to addr in this process and reports the stacks of those that tried.
func GuardDialsTo(addr string) (attempts func() []string) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		panic("http.DefaultTransport is not an *http.Transport")
	}
	var mu sync.Mutex
	var stacks []string
	next := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
		if target == addr {
			mu.Lock()
			stacks = append(stacks, string(debug.Stack()))
			mu.Unlock()
			return nil, fmt.Errorf("a test dialed %s, the port a person's own Agenthail listens on", addr)
		}
		return next(ctx, network, target)
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), stacks...)
	}
}

// FreeLoopbackAddress names a loopback port nothing is listening on.
func FreeLoopbackAddress() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	return listener.Addr().String()
}

// RunAwayFromLiveDashboard points the default dashboard address at a free port for
// the whole test binary and fails the binary if anything still dials the real one.
func RunAwayFromLiveDashboard(run func() int, standard string, defaultListen *string) int {
	return runAwayFromLiveDashboard(run, standard, defaultListen, os.Stderr)
}

func runAwayFromLiveDashboard(run func() int, standard string, defaultListen *string, report io.Writer) int {
	*defaultListen = FreeLoopbackAddress()
	attempts := GuardDialsTo(standard)
	code := run()
	if stacks := attempts(); len(stacks) > 0 {
		fmt.Fprintf(report, "FAIL: %d dial(s) reached %s during tests. The transport dials on its own goroutine, so run with -v to see which test was running:\n%s\n", len(stacks), standard, stacks[0])
		return 1
	}
	return code
}
