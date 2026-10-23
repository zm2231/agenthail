package daemon

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
	"github.com/zm2231/agenthail/internal/testkit"
)

// TestALargeTranscriptMovesBetweenTwoDaemonProcesses moves a session whose
// transcript is AGENTHAIL_MOVE_BYTES long from one daemon process to another
// and reports each process's peak resident memory. It runs only when
// AGENTHAIL_MOVE_ROLE names the process's side (target, then source) and
// AGENTHAIL_MOVE_DIR is a folder both processes share.
func TestALargeTranscriptMovesBetweenTwoDaemonProcesses(t *testing.T) {
	role, dir := os.Getenv("AGENTHAIL_MOVE_ROLE"), os.Getenv("AGENTHAIL_MOVE_DIR")
	if role == "" || dir == "" {
		t.Skip("set AGENTHAIL_MOVE_ROLE and AGENTHAIL_MOVE_DIR to measure a large move across two processes")
	}
	size, err := strconv.ParseInt(os.Getenv("AGENTHAIL_MOVE_BYTES"), 10, 64)
	if err != nil || size <= 0 {
		size = 900 << 20
	}
	t.Setenv("HOME", filepath.Join(dir, role+"-home"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	claudetest.New(t)
	node := newMoveNode(t)
	handoff := filepath.Join(dir, "target.json")
	switch role {
	case "target":
		origin := filepath.Join(dir, "repo")
		if err := os.MkdirAll(origin, 0700); err != nil {
			t.Fatal(err)
		}
		runGit(t, origin, "init", "--quiet", "--initial-branch=main")
		if err := os.WriteFile(filepath.Join(origin, "README"), []byte("widget\n"), 0600); err != nil {
			t.Fatal(err)
		}
		runGit(t, origin, "add", "README")
		runGit(t, origin, "commit", "--quiet", "-m", "start")
		checkout := filepath.Join(node.host.Home, "checkouts", "widget")
		runGit(t, node.host.Home, "clone", "--quiet", filepath.Join(dir, "repo"), checkout)
		runGit(t, checkout, "remote", "set-url", "origin", moveRemote)
		if err := node.reg.AddProject(registry.ProjectRecord{ID: "project-widget", WorkspaceRoot: checkout, Title: "widget", Added: true}); err != nil {
			t.Fatal(err)
		}
		node.serve(t, nil)
		cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: node.server.Certificate().Raw})
		encoded, _ := json.Marshal(map[string]string{"invite": node.invite(t), "address": node.address, "cert": string(cert)})
		if err := os.WriteFile(handoff+".tmp", encoded, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(handoff+".tmp", handoff); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Hour)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(filepath.Join(dir, "source.done")); err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		moves, _ := node.reg.SessionMoves(false)
		for _, move := range moves {
			if session, err := node.reg.Session(move.Conversation); err == nil {
				info, _ := os.Stat(claudeTranscriptIn(t, node.host.Home, session.ID))
				var placed int64
				if info != nil {
					placed = info.Size()
				}
				t.Logf("target: move %s %s; transcript here is %d bytes", move.ID, move.State, placed)
			}
		}
		reportPeakRSS(t, "target")
	case "source":
		for {
			if _, err := os.Stat(handoff); err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		runGit(t, node.host.Home, "clone", "--quiet", filepath.Join(dir, "repo"), node.host.Cwd)
		runGit(t, node.host.Cwd, "remote", "set-url", "origin", moveRemote)
		data, err := os.ReadFile(handoff)
		if err != nil {
			t.Fatal(err)
		}
		var target map[string]string
		if err := json.Unmarshal(data, &target); err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode([]byte(target["cert"]))
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		roots.AddCert(certificate)
		node.start(t, roots)
		response := node.request(t, "POST", "/api/v1/hosts", map[string]string{"pairingUrl": target["invite"], "address": target["address"], "name": "server"})
		if response.Code != 201 {
			t.Fatalf("pair status=%d body=%s", response.Code, response.Body.String())
		}
		record, err := node.reg.Host("server")
		if err != nil {
			t.Fatal(err)
		}
		testkit.Eventually(t, "the target connected", 10*time.Second, func() bool { return node.daemon.hostConnected(record.ID) })
		session := startMovable(t, node)
		transcript := claudeTranscriptIn(t, node.host.Home, session.ID)
		padTranscript(t, transcript, session.ID, size)
		reportPeakRSS(t, "source before the move")
		started := time.Now()
		result, err := node.daemon.moveSession(context.Background(), sessionMoveRequest{SessionID: session.ID, Host: "server", WaitSeconds: 60})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("source: moved %d bytes in %s; move %s %s", size, time.Since(started).Round(time.Millisecond), result.Move.ID, result.Move.State)
		reportPeakRSS(t, "source")
		_ = os.WriteFile(filepath.Join(dir, "source.done"), nil, 0600)
	default:
		t.Fatalf("unknown role %q", role)
	}
}

// padTranscript grows a transcript to size with assistant records, the
// shape a long conversation's transcript has.
func padTranscript(t *testing.T, path, conversation string, size int64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, _ := file.Stat()
	text := strings.Repeat("x", 4000)
	for written, line := info.Size(), 0; written < size; line++ {
		record := fmt.Sprintf(`{"type":"assistant","sessionId":%q,"uuid":"pad-%d","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}`+"\n", conversation, line, text)
		n, err := file.WriteString(record)
		if err != nil {
			t.Fatal(err)
		}
		written += int64(n)
	}
	closing := fmt.Sprintf(`{"type":"system","subtype":"turn_duration","sessionId":%q}`+"\n", conversation)
	if _, err := file.WriteString(closing); err != nil {
		t.Fatal(err)
	}
}

func reportPeakRSS(t *testing.T, what string) {
	t.Helper()
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	peak := usage.Maxrss
	if runtime.GOOS == "linux" {
		peak *= 1024
	}
	t.Logf("%s: peak resident memory %.1f MiB", what, float64(peak)/(1<<20))
}
