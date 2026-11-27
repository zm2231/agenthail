package daemon

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

func ageInflight(t *testing.T, r *registry.Registry, table string, id int64, by time.Duration) {
	t.Helper()
	db, err := sql.Open("sqlite", r.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=10000`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE `+table+` SET inflight_at_ms=? WHERE id=?`, time.Now().Add(-by).UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
}

func TestAHeldMessageSentLongerAgoThanHostsKeepReceiptsIsNotSentAgain(t *testing.T) {
	local, remote, record := pairedFederation(t)
	host, _ := local.daemon.pairedHost(record.ID)
	host.drain.Lock()
	remote.gate.drops.Store(2)
	send := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": hosts.Qualify(record.ID, "remote-a"), "message": "sent before a long outage"})
	if send.Code != http.StatusOK || !strings.Contains(send.Body.String(), `"evidence":"unknown"`) {
		t.Fatalf("lost answer status=%d body=%s", send.Code, send.Body.String())
	}
	remote.gate.drops.Store(0)
	queue, err := local.reg.ListQueue(true, nil)
	if err != nil || len(queue) != 1 {
		t.Fatalf("queue=%+v err=%v", queue, err)
	}
	ageInflight(t, local.reg, "message_queue", queue[0].ID, heldReplayLimit+time.Hour)
	host.drain.Unlock()
	local.daemon.drainHostDeliveries(context.Background(), host)
	if got := remote.fake.delivered(); len(got) != 1 {
		t.Fatalf("an expired held message was sent again: delivered=%v", got)
	}
	queue, err = local.reg.ListQueue(true, nil)
	if err != nil || len(queue) != 1 || queue[0].Status != "dead" || !strings.Contains(queue[0].LastError, "unknown") {
		t.Fatalf("queue after the drain=%+v err=%v, want it dead with its outcome unknown", queue, err)
	}
}

func TestAHeldHostActionSentLongerAgoThanHostsKeepReceiptsIsNotSentAgain(t *testing.T) {
	local, remote, record := pairedFederation(t)
	host, _ := local.daemon.pairedHost(record.ID)
	host.drain.Lock()
	request := []byte(`{"action":"interrupt","sessionId":"remote-a"}`)
	id, err := local.reg.HoldHostAction(record.ID, "held-old", "stop remote-a", request, true)
	if err != nil {
		t.Fatal(err)
	}
	ageInflight(t, local.reg, "host_held_actions", id, heldReplayLimit+time.Hour)
	host.drain.Unlock()
	local.daemon.drainHostHeldActions(context.Background(), host)
	if got := remote.fake.interrupts(); got != 0 {
		t.Fatalf("an expired held action was sent: interrupts=%d", got)
	}
	if held, err := local.reg.HostHeldActions(record.ID); err != nil || len(held) != 0 {
		t.Fatalf("held=%+v err=%v, want it settled", held, err)
	}
	history, err := local.reg.ListHistory(20, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history {
		if entry.Result == "failed" && strings.Contains(entry.Error, "may not have happened") {
			return
		}
	}
	t.Fatalf("no failed history entry for the expired action: %+v", history)
}
