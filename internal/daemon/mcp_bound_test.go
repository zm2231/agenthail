package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func holdRegistryWriter(t *testing.T, d *Daemon) (release func()) {
	t.Helper()
	held, err := d.Registry.WriteDB().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { held.Close() }) }
	t.Cleanup(release)
	return release
}

type mcpAnswer struct {
	out     map[string]any
	refused bool
}

func callWithin(t *testing.T, call func() (map[string]any, bool), within time.Duration, what string) mcpAnswer {
	t.Helper()
	answered := make(chan mcpAnswer, 1)
	go func() {
		out, refused := call()
		answered <- mcpAnswer{out, refused}
	}()
	select {
	case got := <-answered:
		return got
	case <-time.After(within):
		t.Fatalf("%s did not answer within %s", what, within)
		return mcpAnswer{}
	}
}

func TestMCPReadsAnswerWhileTheRegistryWriterIsHeld(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	logged := &lockedBuffer{}
	d.log = log.New(logged, "", 0)
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, from))
	holdRegistryWriter(t, d)
	for _, tool := range []string{"agenthail_whoami", "agenthail_requests", "agenthail_sessions"} {
		got := callWithin(t, func() (map[string]any, bool) { return callMCP(t, session, tool, nil) }, 5*time.Second, tool+" while the writer was held")
		if got.refused {
			t.Fatalf("%s while the writer was held was refused: %v", tool, got.out)
		}
	}
	// A session never read has no journal yet, and starting one is a write:
	// the read answers within its catch-up wait, as busy.
	got := callWithin(t, func() (map[string]any, bool) {
		return callMCP(t, session, "agenthail_read", map[string]any{"sessionId": from.ID})
	}, 5*time.Second, "agenthail_read while the writer was held")
	if got.refused && (got.out["code"] != "busy" || got.out["retryable"] != true) {
		t.Fatalf("agenthail_read while the writer was held = %v, want an answer or a retryable busy refusal", got.out)
	}
	if !strings.Contains(logged.String(), "mcp agenthail_whoami session=from path=http took=") || !strings.Contains(logged.String(), "outcome=ok") {
		t.Fatalf("the daemon log does not name each call:\n%s", logged.String())
	}
}

func TestAReadPastItsBoundIsRefusedAsBusyAndWorksOnceTheDaemonCatchesUp(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	previous := mcpReadBound
	mcpReadBound = 300 * time.Millisecond
	t.Cleanup(func() { mcpReadBound = previous })
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, from))
	release := holdRegistryWriter(t, d)
	read := func() (map[string]any, bool) {
		return callMCP(t, session, "agenthail_read", map[string]any{"sessionId": from.ID})
	}
	got := callWithin(t, read, 5*time.Second, "a read past its bound")
	if !got.refused || got.out["code"] != "busy" || got.out["retryable"] != true {
		t.Fatalf("a read past its bound = %v (refused %v), want a retryable busy refusal", got.out, got.refused)
	}
	release()
	mcpReadBound = previous
	if out, refused := read(); refused {
		t.Fatalf("a read once the writer was free was refused: %v", out)
	}
}

func TestAWritePastItsBoundReportsAnUnknownOutcomeAndItsRetryReadsTheResult(t *testing.T) {
	d, _, _, _, _, _ := questionDaemon(t)
	previous := mcpWriteBound
	mcpWriteBound = 300 * time.Millisecond
	t.Cleanup(func() { mcpWriteBound = previous })
	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, *worker))
	write := func() (map[string]any, bool) {
		return callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": "slow-add", "request": map[string]any{"action": "add", "text": "fix the flag", "fields": map[string]any{"type": "bug", "priority": "P1"}}})
	}
	release := holdRegistryWriter(t, d)
	got := callWithin(t, write, 5*time.Second, "a write past its bound")
	detail, _ := got.out["detail"].(map[string]any)
	if !got.refused || got.out["code"] != "busy" || got.out["retryable"] != true || detail["outcome"] != "unknown" {
		t.Fatalf("a write past its bound = %v (refused %v), want a retryable busy refusal with an unknown outcome", got.out, got.refused)
	}
	release()
	mcpWriteBound = previous
	var out map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for {
		var refused bool
		out, refused = write()
		if !refused {
			break
		}
		if detail, _ := out["detail"].(map[string]any); detail["outcome"] != "unknown" || time.Now().After(deadline) {
			t.Fatalf("the retry with the same clientRequestId = %v", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	item, _ := out["item"].(map[string]any)
	if item["id"] != "widget-W1" {
		t.Fatalf("the retry = %v, want the one item the first call added", out)
	}
	next, refused := callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": "next-add", "request": map[string]any{"action": "add", "text": "another", "fields": map[string]any{"type": "bug", "priority": "P2"}}})
	if item, _ := next["item"].(map[string]any); refused || item["id"] != "widget-W2" {
		t.Fatalf("the next add = %v, want widget-W2 after the slow add was applied once", next)
	}
}

func TestASocketCallAnswersWhileTheRegistryWriterIsHeldAndIsLoggedAsSocket(t *testing.T) {
	d, reg, root, _ := mcpSocketDaemon(t)
	logged := &lockedBuffer{}
	d.log = log.New(logged, "", 0)
	registerClaude(t, reg, "claude-one")
	parent := &relayedSession{record: filepath.Join(root, "sessions", strconv.Itoa(os.Getppid())+".json")}
	parent.writeFor(t, os.Getppid(), "claude-one", true)
	client := socketClient(t)
	holdRegistryWriter(t, d)
	got := callWithin(t, func() (map[string]any, bool) { return callMCP(t, client, "agenthail_whoami", nil) }, 15*time.Second, "agenthail_whoami over the socket while the writer was held")
	if got.refused && (got.out["code"] != "busy" || got.out["retryable"] != true) {
		t.Fatalf("whoami over the socket = %v, want an answer or a retryable busy refusal", got.out)
	}
	if !strings.Contains(logged.String(), "mcp agenthail_whoami session=claude-one path=socket") {
		t.Fatalf("the daemon log does not name the socket call:\n%s", logged.String())
	}
}

func TestWorkThatFinishesWithinItsBoundAlwaysReturnsItsResult(t *testing.T) {
	d := &Daemon{log: log.New(&lockedBuffer{}, "", 0)}
	var wrong sync.Map
	var calls sync.WaitGroup
	for range 64 {
		calls.Go(func() {
			for range 500 {
				got, err := withinBound(d, context.Background(), "test", time.Minute, func(context.Context) (int, error) { return 1, nil })
				if err != nil || got != 1 {
					wrong.Store(err, got)
				}
			}
		})
	}
	calls.Wait()
	wrong.Range(func(err, got any) bool {
		t.Errorf("work that finished at once came back as %v, %v", got, err)
		return true
	})
}

func TestAWriteInterruptedBeforeItsOutcomeWasRecordedIsUnknownAndNotRetryable(t *testing.T) {
	d, _, _, _, _, _ := questionDaemon(t)
	worker, _ := d.Registry.Session("s1")
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, *worker))
	write := func() (map[string]any, bool) {
		return callMCP(t, session, "agenthail_ledger_write", map[string]any{"op": "work", "clientRequestId": "cut-add", "request": map[string]any{"action": "add", "text": "fix the flag", "fields": map[string]any{"type": "bug", "priority": "P1"}}})
	}
	if out, refused := write(); refused {
		t.Fatalf("first write refused: %v", out)
	}
	db, err := sql.Open("sqlite", d.Registry.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=10000`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE action_receipts SET status='pending', receipt=x''`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		out, refused := write()
		detail, _ := out["detail"].(map[string]any)
		if !refused || out["code"] != "busy" || out["retryable"] != false || detail["outcome"] != "unknown" {
			t.Fatalf("retry of an interrupted write = %v (refused %v), want a busy refusal with an unknown outcome that is not retryable", out, refused)
		}
	}
}
