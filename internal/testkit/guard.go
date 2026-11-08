package testkit

import (
	"fmt"
	"io"
	"net"
	"os"
)

// GuardDialsTo refuses HTTP dials to addr in this process until restore is called, and reports the stacks of those that tried.
func GuardDialsTo(addr string) (attempts func() []string, restore func()) {
	guard := &dialGuard{addr: addr}
	dialGuards.add(guard)
	return guard.attempts, func() { dialGuards.remove(guard) }
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

var freeAddress = FreeLoopbackAddress

func runAwayFromLiveDashboard(run func() int, standard string, defaultListen *string, report io.Writer) int {
	*defaultListen = freeAddress()
	for tries := 1; *defaultListen == standard; tries++ {
		if tries == 100 {
			fmt.Fprintf(report, "FAIL: every free loopback port offered in %d tries was %s, the real dashboard address\n", tries, standard)
			return 1
		}
		*defaultListen = freeAddress()
	}
	attempts, restore := GuardDialsTo(standard)
	defer restore()
	code := run()
	if stacks := attempts(); len(stacks) > 0 {
		fmt.Fprintf(report, "FAIL: %d dial(s) reached %s during tests. The transport dials on its own goroutine, so run with -v to see which test was running:\n%s\n", len(stacks), standard, stacks[0])
		return 1
	}
	return code
}
