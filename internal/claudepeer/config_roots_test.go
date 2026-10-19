package claudepeer

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestNativeSendAuthenticatesWithTheKeyInTheRootHoldingTheTargetRecord(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudetest.New(t)
	root := fixture.AddRoot("claude-work")
	socket := claudetest.ClaimSocket(t, fixture.PID)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	first := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		first <- line
	}()
	pid := strconv.Itoa(fixture.PID)
	record, _ := json.Marshal(map[string]any{"pid": fixture.PID, "sessionId": "target", "messagingSocketPath": socket})
	hash := sha256.Sum256([]byte(socket))
	for path, content := range map[string]string{
		filepath.Join(root, "sessions", pid+".json"):                                string(record),
		filepath.Join(root, "sessions", pid+"."+hex.EncodeToString(hash[:])+".key"): `{"peerToken":"second-root-token"}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sender := surface.Session{ID: "sender", Surface: surface.KindClaude}
	if _, err := SendNative(context.Background(), fixture.Home, sender, "/tmp/cc-socks/1.sock", socket, "hello"); err != nil {
		t.Fatal(err)
	}
	var auth struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(<-first), &auth); err != nil || auth.Type != "auth" || auth.Token != "second-root-token" {
		t.Fatalf("first frame auth=%+v err=%v, want the second root's peer key", auth, err)
	}
}
