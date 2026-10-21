package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const sharedConversation = "conversation-shared"

// sharedConversationFixture is one conversation open in two live Claude
// processes, as when it is resumed or branched into a second terminal, and a
// native sender process that writes to it.
type sharedConversationFixture struct {
	fixture *claudetest.Fixture
	adapter *Claude
	first   *claudetest.Holder
	later   *claudetest.Holder
	ctx     context.Context
}

func newSharedConversation(t *testing.T) sharedConversationFixture {
	t.Helper()
	fixture := claudetest.New(t)
	cwd := t.TempDir()
	fixture.Transcript(sharedConversation, cwd)
	first := fixture.Holder(sharedConversation, cwd)
	later := fixture.Holder(sharedConversation, cwd)
	writeNativeSender(t, fixture.Root, "sender-native")
	return sharedConversationFixture{fixture: fixture, adapter: NewClaude(nil, fixture.Home), first: first, later: later, ctx: surface.WithSourceSessionID(context.Background(), "sender-native")}
}

// writeNativeSender records the test process as a Claude session with its own
// messaging socket, so a send names it as its sender.
func writeNativeSender(t *testing.T, root, id string) {
	t.Helper()
	socket := claudetest.ClaimSocket(t, os.Getpid())
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(os.Getpid()))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	start, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	record := map[string]any{"pid": os.Getpid(), "sessionId": id, "name": id, "cwd": "/fixture", "status": "idle", "version": "2.1.270", "procStart": string(start), "messagingSocketPath": socket}
	data, _ := json.Marshal(record)
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sessions", strconv.Itoa(os.Getpid())+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// listedHolders reads the processes a listed session names through its JSON
// form, which is what the catalog and API carry.
func listedHolders(t *testing.T, session surface.Session) []int {
	t.Helper()
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Holders []struct {
			PID    int    `json:"pid"`
			Status string `json:"status"`
		} `json:"holders"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	pids := make([]int, 0, len(decoded.Holders))
	for _, holder := range decoded.Holders {
		if holder.Status == "" {
			t.Fatalf("holder %d has no status: %s", holder.PID, data)
		}
		pids = append(pids, holder.PID)
	}
	return pids
}

func (s sharedConversationFixture) listShared(t *testing.T) surface.Session {
	t.Helper()
	sessions, err := s.adapter.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found []surface.Session
	for _, session := range sessions {
		if session.ID == sharedConversation {
			found = append(found, session)
		}
	}
	if len(found) != 1 {
		t.Fatalf("listed %d rows for the shared conversation, want one: %+v", len(found), sessions)
	}
	return found[0]
}

// assertNothingReceived waits long enough for a stray frame to land and
// fails when either process took one.
func (s sharedConversationFixture) assertNothingReceived(t *testing.T) {
	t.Helper()
	time.Sleep(200 * time.Millisecond)
	if first, later := s.first.Received(), s.later.Received(); len(first) != 0 || len(later) != 0 {
		t.Fatalf("a refused write reached a process: first=%q later=%q", first, later)
	}
}

func assertNamesBothHolders(t *testing.T, err error, first, later int) {
	t.Helper()
	if err == nil || !surface.IsDeliveryRefused(err) {
		t.Fatalf("err=%v, want a refusal that holds a queued message", err)
	}
	for _, pid := range []int{first, later} {
		if !strings.Contains(err.Error(), fmt.Sprintf("claude:%d", pid)) {
			t.Fatalf("refusal %q does not name the pin for process %d", err, pid)
		}
	}
}

func TestClaudeConversationOpenInTwoProcessesListsBothHolders(t *testing.T) {
	s := newSharedConversation(t)
	listed := s.listShared(t)
	if listed.PID != 0 || listed.Bridge != "" {
		t.Fatalf("listed=%+v, want no single process chosen for a conversation two processes hold", listed)
	}
	holders := listedHolders(t, listed)
	if len(holders) != 2 || holders[0] != s.first.PID || holders[1] != s.later.PID {
		t.Fatalf("holders=%v, want [%d %d]", holders, s.first.PID, s.later.PID)
	}
}

func TestClaudeWriteToAConversationOpenInTwoProcessesIsRefusedNamingEach(t *testing.T) {
	s := newSharedConversation(t)
	// A row bound to the first process before the second opened the
	// conversation, as the registry kept it.
	bound := func() *surface.Session {
		return &surface.Session{ID: sharedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: s.first.PID, Transport: "uds"}
	}
	listed := s.listShared(t)
	for name, write := range map[string]func(*surface.Session) error{
		"send": func(session *surface.Session) error {
			_, err := s.adapter.Send(s.ctx, session, "which terminal?")
			return err
		},
		"steer":     func(session *surface.Session) error { return s.adapter.Steer(s.ctx, session, "which terminal?") },
		"interrupt": func(session *surface.Session) error { return s.adapter.Interrupt(s.ctx, session) },
	} {
		for label, session := range map[string]*surface.Session{"bound": bound(), "listed": &listed} {
			t.Run(name+"/"+label, func(t *testing.T) {
				addressed := *session
				assertNamesBothHolders(t, write(&addressed), s.first.PID, s.later.PID)
			})
		}
	}
	s.assertNothingReceived(t)
}

func TestClaudeWriteToAPinnedHolderReachesOnlyThatProcess(t *testing.T) {
	s := newSharedConversation(t)
	pinned, err := s.adapter.Resolve(context.Background(), strconv.Itoa(s.later.PID))
	if err != nil {
		t.Fatal(err)
	}
	if pinned.ID != sharedConversation {
		t.Fatalf("pinned=%+v", pinned)
	}
	if result, err := s.adapter.Send(s.ctx, pinned, "to the later terminal"); err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	waitForFrames(t, s.later, 1)
	if first := s.first.Received(); len(first) != 0 {
		t.Fatalf("the first process took a message pinned to the later one: %q", first)
	}
}

func TestClaudePinToAProcessThatLeftTheConversationIsRefused(t *testing.T) {
	s := newSharedConversation(t)
	pinned, err := s.adapter.Resolve(context.Background(), strconv.Itoa(s.later.PID))
	if err != nil {
		t.Fatal(err)
	}
	s.later.Record("conversation-elsewhere", "idle")
	_, err = s.adapter.Send(s.ctx, pinned, "stale pin")
	if !surface.IsDeliveryTerminal(err) || surface.DeliveryTerminalReason(err) != surface.DeliveryTargetMissing {
		t.Fatalf("err=%v, want target missing", err)
	}
	s.assertNothingReceived(t)
}

func TestClaudeConversationBackToOneProcessTakesWritesAgain(t *testing.T) {
	s := newSharedConversation(t)
	s.first.Exit()
	listed := s.listShared(t)
	if listed.PID != s.later.PID || len(listedHolders(t, listed)) != 0 {
		t.Fatalf("listed=%+v, want the remaining process attached and no holder list", listed)
	}
	if result, err := s.adapter.Send(s.ctx, &listed, "only one left"); err != nil || result == nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	waitForFrames(t, s.later, 1)
}

func waitForFrames(t *testing.T, holder *claudetest.Holder, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(holder.Received()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("process %d received %d messages, want %d", holder.PID, len(holder.Received()), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClaudeConversationOpenInTwoProcessesIsOpenAndWaitsWhenEitherWaits(t *testing.T) {
	s := newSharedConversation(t)
	listed := s.listShared(t)
	if open := s.adapter.OpenSessions(context.Background(), []surface.Session{listed}); !open[sharedConversation] {
		t.Fatalf("open=%v, want a conversation two live processes hold counted open", open)
	}
	s.later.Record(sharedConversation, "waiting")
	status, err := s.adapter.LocalStatus(context.Background(), listed)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != surface.StatusBusy || status.WaitingOn == nil {
		t.Fatalf("status=%+v, want the waiting holder to make the conversation wait", status)
	}
}
