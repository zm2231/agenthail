package claudepeer

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAuthenticatedFrameUsesAlternateRoot(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "ah-auth-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	target := filepath.Join(root, "123.sock")
	ln, err := net.Listen("unix", target)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	defaultDir, alternate := filepath.Join(root, "native"), filepath.Join(root, "gentle")
	if err := os.MkdirAll(filepath.Join(alternate, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(target))
	keyName := "123." + hex.EncodeToString(hash[:]) + ".key"
	if err := os.WriteFile(filepath.Join(alternate, "sessions", keyName), []byte(`{"peerToken":"fixture-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- err
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		if !scanner.Scan() {
			got <- scanner.Err()
			return
		}
		var auth map[string]string
		err = json.Unmarshal(scanner.Bytes(), &auth)
		if err == nil && (auth["type"] != "auth" || auth["token"] != "fixture-token") {
			err = net.ErrClosed
		}
		if err == nil && !scanner.Scan() {
			err = net.ErrClosed
		}
		got <- err
	}()
	if err := sendAuthenticatedFrame([]string{defaultDir, alternate}, target, []byte(`{"type":"user"}`)); err != nil {
		t.Fatal(err)
	}
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(defaultDir, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultDir, "sessions", keyName), []byte(`{"peerToken":"another-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sendAuthenticatedFrame([]string{defaultDir, alternate}, target, []byte(`{}`)); !surface.IsDeliveryTerminal(err) {
		t.Fatalf("duplicate key err=%v", err)
	}
	if err := os.Remove(filepath.Join(defaultDir, "sessions", keyName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(alternate, "sessions", keyName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alternate, "sessions", "123.json"), []byte(`{"pid":123,"sessionId":"gentle"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sendAuthenticatedFrame([]string{defaultDir, alternate}, target, []byte(`{}`)); !surface.IsDeliveryTerminal(err) {
		t.Fatalf("missing key err=%v", err)
	}
}
