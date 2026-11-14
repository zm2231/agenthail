//go:build linux || darwin

package peerproc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestTheKernelNamesTheProcessOnTheOtherEndAndItsParent(t *testing.T) {
	dir, err := os.MkdirTemp("", "peer")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	listener, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dialed, err := net.Dial("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer dialed.Close()
	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	peer, err := Of(accepted)
	if err != nil || peer.PID != os.Getpid() || peer.UID != os.Getuid() {
		t.Fatalf("peer = %+v, %v; want pid %d uid %d", peer, err, os.Getpid(), os.Getuid())
	}
	if parent, err := Parent(os.Getpid()); err != nil || parent != os.Getppid() {
		t.Fatalf("Parent(self) = %d, %v; want %d", parent, err, os.Getppid())
	}
}
