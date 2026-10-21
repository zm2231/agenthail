package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

// nativeSenderClaude is the Claude adapter with every send made from a native
// Claude session of the test process, so a message reaches a holder's
// messaging socket directly rather than through the peer manager, which
// runDaemon does not start.
type nativeSenderClaude struct {
	*surfaces.Claude
	source string
}

func (c nativeSenderClaude) Send(ctx context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	return c.Claude.Send(surface.WithSourceSessionID(ctx, c.source), session, message)
}

func recordNativeSender(t *testing.T, root, id string) {
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

func snapshotHolders(t *testing.T, h *runningDaemon, id string) []int {
	t.Helper()
	status, payload := h.request(http.MethodGet, "/api/v1/snapshot", nil)
	if status != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", status, payload)
	}
	var decoded struct {
		Sessions []struct {
			ID      string `json:"id"`
			Holders []struct {
				PID int `json:"pid"`
			} `json:"holders"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, session := range decoded.Sessions {
		if session.ID == id {
			pids := []int{}
			for _, holder := range session.Holders {
				pids = append(pids, holder.PID)
			}
			return pids
		}
	}
	return nil
}

func TestConversationResumedInASecondTerminalIsNeverSentToOneOfThemSilently(t *testing.T) {
	fixture := claudetest.New(t)
	cwd := t.TempDir()
	const conversation, started = "conversation-scope", "conversation-started"
	fixture.Transcript(conversation, cwd)
	first := fixture.Holder(conversation, cwd)
	later := fixture.Holder(started, cwd)
	recordNativeSender(t, fixture.Root, "sender-native")
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	claude := nativeSenderClaude{Claude: surfaces.NewClaude(nil, fixture.Home), source: "sender-native"}
	h, _ := runDaemon(t, store, claude)

	eventually(t, "the later process's starting conversation to be registered", func() bool {
		session, err := store.Session(started)
		return err == nil && session.PID == later.PID
	})
	later.Record(conversation, "idle")
	eventually(t, "the conversation to list both processes", func() bool {
		holders := snapshotHolders(t, h, conversation)
		return len(holders) == 2 && holders[0] == first.PID && holders[1] == later.PID
	})
	eventually(t, "the row the later process started under to be removed", func() bool {
		h.daemon.requestCatalogDiscovery()
		_, err := store.Session(started)
		return errors.Is(err, sql.ErrNoRows)
	})

	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": conversation, "author": "person", "message": "which terminal?"})
	if status != http.StatusConflict {
		t.Fatalf("person send status=%d body=%s, want a refusal", status, payload)
	}
	for _, pid := range []int{first.PID, later.PID} {
		if !strings.Contains(string(payload), fmt.Sprintf("claude:%d", pid)) {
			t.Fatalf("refusal %s does not name process %d", payload, pid)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if len(first.Received()) != 0 || len(later.Received()) != 0 {
		t.Fatalf("a refused send reached a process: first=%q later=%q", first.Received(), later.Received())
	}

	status, payload = h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": conversation, "author": "person", "message": "picked before the pid was reused", "pid": later.PID, "pidStartedAt": 1})
	if status != http.StatusConflict {
		t.Fatalf("send pinned to an earlier process under pid %d: status=%d body=%s, want a refusal", later.PID, status, payload)
	}
	h.receipt(map[string]any{"action": "send", "sessionId": conversation, "author": "person", "message": "to the later terminal", "pid": later.PID})
	eventually(t, "the pinned send to reach the later process", func() bool { return len(later.Received()) == 1 })
	if len(first.Received()) != 0 {
		t.Fatalf("a send pinned to process %d reached process %d: %q", later.PID, first.PID, first.Received())
	}

	queued, err := store.QueueMessageWithOptions(conversation, "after one terminal closes", "", surface.SendOptions{BusyDelivery: "queue"})
	if err != nil {
		t.Fatal(err)
	}
	h.awaitQueueItem(queued, "the queued message to be held naming both processes", func(item dashboardQueue) bool {
		return !item.Historical && strings.Contains(item.LastError, "delivers once only one terminal is left open") && strings.Contains(item.LastError, "send it fresh") && strings.Contains(item.LastError, fmt.Sprintf("claude:%d", first.PID)) && strings.Contains(item.LastError, fmt.Sprintf("claude:%d", later.PID))
	})
	if len(first.Received()) != 0 || len(later.Received()) != 1 {
		t.Fatalf("a held message reached a process: first=%q later=%q", first.Received(), later.Received())
	}
	first.Exit()
	h.execStore(`UPDATE message_queue SET available_at_ms=0 WHERE id=?`, queued)
	h.awaitQueueItem(queued, "the held message to reach the remaining process", func(item dashboardQueue) bool {
		return item.Historical && item.Status == "delivered"
	})
	if received := later.Received(); len(received) != 2 || !strings.Contains(received[1], "after one terminal closes") {
		t.Fatalf("later received %q", received)
	}
}
