package claudepeer

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestTheSocketDirOverrideReplacesEveryClaudeSocketDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(SocketDirEnv, dir)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if got := ClaudeSocketDirs(); !slices.Equal(got, []string{dir}) {
		t.Fatalf("socket dirs=%v, want only %s", got, dir)
	}
	if got := SocketDir(); got != dir {
		t.Fatalf("socket dir=%s, want %s", got, dir)
	}
}

func TestWithTheSocketDirOverrideNothingDialsTheSharedClaudeSockets(t *testing.T) {
	// A unix socket path must stay under about 104 bytes, longer than most test temp directories allow.
	dir, err := os.MkdirTemp("/tmp", "ahs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv(SocketDirEnv, dir)
	if err := os.MkdirAll(defaultSocketDir, 0700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(defaultSocketDir, strconv.Itoa(os.Getpid())+".sock")
	if _, err := os.Lstat(shared); !os.IsNotExist(err) {
		t.Fatalf("test target socket already exists: %v", err)
	}
	listener, err := net.Listen("unix", shared)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dialed := make(chan struct{}, 1)
	go func() {
		if conn, err := listener.Accept(); err == nil {
			conn.Close()
			dialed <- struct{}{}
		}
	}()
	sender := surface.Session{ID: "sender", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "sender"}
	senderSocket := filepath.Join(SocketDir(), "agenthail-sender-"+uuid.NewString()[:8]+".sock")
	senderListener, err := net.Listen("unix", senderSocket)
	if err != nil {
		t.Fatal(err)
	}
	defer senderListener.Close()
	if _, err := SendNative(context.Background(), t.TempDir(), sender, senderSocket, shared, "", "hello"); err == nil {
		t.Fatal("a send to a socket in the shared Claude directory was accepted")
	}
	select {
	case <-dialed:
		t.Fatal("a send dialed a socket in the shared Claude directory")
	case <-time.After(300 * time.Millisecond):
	}
}
