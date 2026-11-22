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
	got := Describe(port)
	if want := "pid " + strconv.Itoa(os.Getpid()) + " ("; !strings.HasPrefix(got, want) {
		t.Fatalf("Describe(%d) = %q, want it to start with %q", port, got, want)
	}
	listener.Close()
	if got := Describe(port); got != "" {
		t.Fatalf("Describe(%d) after close = %q, want nothing", port, got)
	}
}
