//go:build linux || darwin

package portholder

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestDescribeNamesTheProcessListeningOnAPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	self := "pid " + strconv.Itoa(os.Getpid()) + " ("
	if got := Describe(port); !strings.HasPrefix(got, self) {
		t.Fatalf("Describe(%d) = %q, want it to start with %q", port, got, self)
	}
	listener.Close()
	// Another process may take the freed ephemeral port at once.
	holders, err := Find(port)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range holders {
		if h.PID == os.Getpid() {
			t.Fatalf("Find(%d) after close = %v, still names this process", port, holders)
		}
	}
}
