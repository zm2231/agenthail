package registry

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAnAgentIdentityTakesOnlyAFreeName(t *testing.T) {
	r := openTestRegistry(t)
	other := surface.Session{ID: "worker-1", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "worker", Status: surface.StatusIdle}
	if err := r.RecordObservedSession(other); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("builder", other.ID); err != nil {
		t.Fatal(err)
	}
	var taken AgentIdentityTakenError
	if _, err := r.AddAgentIdentity("builder"); !errors.As(err, &taken) || taken.Owner != other.ID {
		t.Fatalf("an alias another session holds: %v", err)
	}
	if _, err := r.AddAgentIdentity("worker-1"); !errors.As(err, &taken) {
		t.Fatalf("a session's own id: %v", err)
	}
	if _, err := r.AddAgentIdentity(LeadAlias); !errors.Is(err, ErrReservedAlias) {
		t.Fatalf("the lead alias: %v", err)
	}
	var invalid AgentIdentityNameError
	for _, name := range []string{"", "Scout", "-scout", "scout bot", "scout::x", strings.Repeat("k", 65)} {
		if _, err := r.AddAgentIdentity(name); !errors.As(err, &invalid) {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	identity, err := r.AddAgentIdentity("scout")
	if err != nil {
		t.Fatal(err)
	}
	host, err := r.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if identity.SessionID != surface.AgentIdentityID(host.ID, "scout") {
		t.Fatalf("identity=%+v", identity)
	}
	if _, err := r.AddAgentIdentity("scout"); err == nil {
		t.Fatal("added scout twice")
	}
	if id, err := r.ResolveTarget("@scout"); err != nil || id != identity.SessionID {
		t.Fatalf("@scout resolves to %q (err %v)", id, err)
	}
	session, err := r.Session(identity.SessionID)
	if err != nil || !surface.IsAgentIdentity(session) || session.Surface != surface.KindAgent {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	if err := r.SetAlias("scout", other.ID); err == nil {
		t.Fatal("another session took the identity's name")
	}
}

func TestRemovingAnIdentityRefusesWhileAnotherSessionHoldsItsName(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.AddAgentIdentity("scout"); err != nil {
		t.Fatal(err)
	}
	other := surface.Session{ID: "worker-1", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "worker", Status: surface.StatusIdle}
	if err := r.RecordObservedSession(other); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveAlias("scout"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("scout", other.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveAgentIdentity("scout"); err == nil || !strings.Contains(err.Error(), "now names session worker-1") {
		t.Fatalf("removed while worker-1 holds @scout: %v", err)
	}
	if err := r.RemoveAlias("scout"); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveAgentIdentity("scout"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AgentIdentityNamed("scout"); !errors.Is(err, ErrNoAgentIdentity) {
		t.Fatalf("scout after removal: %v", err)
	}
	if _, err := r.AddAgentIdentity("scout"); err != nil {
		t.Fatalf("scout could not be added again: %v", err)
	}
}

func TestAnInboxKeepsAMessageOnceAndHandsItToOneReader(t *testing.T) {
	r := openTestRegistry(t)
	identity, err := r.AddAgentIdentity("scout")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for index, text := range []string{"first", "second"} {
		at := time.Now()
		if index == 0 {
			at = old
		}
		if _, stored, err := r.StoreInboxMessage(InboxMessage{Identity: identity.SessionID, UUID: text, SenderID: surface.CLISessionID, SenderName: "Agenthail CLI", Message: text, ReceivedAt: at}); err != nil || !stored {
			t.Fatalf("store %s: stored=%v err=%v", text, stored, err)
		}
	}
	if _, stored, err := r.StoreInboxMessage(InboxMessage{Identity: identity.SessionID, UUID: "first", Message: "first again"}); err != nil || stored {
		t.Fatalf("a replay of the same message was stored again: stored=%v err=%v", stored, err)
	}
	if unread, err := r.UnreadInbox("scout"); err != nil || unread != 2 {
		t.Fatalf("unread=%d err=%v", unread, err)
	}
	peeked, err := r.ReadInbox("scout", InboxQuery{Peek: true})
	if err != nil || len(peeked) != 2 || !peeked[0].ReadAt.IsZero() {
		t.Fatalf("peek=%+v err=%v", peeked, err)
	}
	read, err := r.ReadInbox("scout", InboxQuery{Limit: 1})
	if err != nil || len(read) != 1 || read[0].Message != "first" || read[0].ReadAt.IsZero() {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	read, err = r.ReadInbox("scout", InboxQuery{})
	if err != nil || len(read) != 1 || read[0].Message != "second" {
		t.Fatalf("second read=%+v err=%v", read, err)
	}
	if read, err = r.ReadInbox("scout", InboxQuery{}); err != nil || len(read) != 0 {
		t.Fatalf("third read=%+v err=%v", read, err)
	}
	if pruned, err := r.PruneInbox("scout", time.Now().Add(-24*time.Hour)); err != nil || pruned != 1 {
		t.Fatalf("pruned=%d err=%v", pruned, err)
	}
	if all, err := r.ReadInbox("scout", InboxQuery{All: true}); err != nil || len(all) != 1 || all[0].Message != "second" {
		t.Fatalf("after prune=%+v err=%v", all, err)
	}
	if _, _, err := r.StoreInboxMessage(InboxMessage{Identity: "agent:nobody", Message: "lost"}); !errors.Is(err, ErrNoAgentIdentity) {
		t.Fatalf("a message to no identity: %v", err)
	}
}

func TestASchema114RegistryUpgradesToAcknowledgedInboxReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := old.AddAgentIdentity("scout")
	if err != nil {
		old.Close()
		t.Fatal(err)
	}
	stored, _, err := old.StoreInboxMessage(InboxMessage{Identity: identity.SessionID, UUID: "m1", Message: "before the upgrade"})
	if err != nil {
		old.Close()
		t.Fatal(err)
	}
	asBeforePresentationTriggers(t, old)
	for _, statement := range []string{`DROP TABLE identity_remote`, `DROP TABLE mcp_access`, `ALTER TABLE agent_inbox DROP COLUMN delivered_at_ms`, `PRAGMA user_version=114`} {
		if _, err := old.db.Exec(statement); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if acked, err := r.AckInbox("scout", stored.ID); err != nil || acked != 0 {
		t.Fatalf("an ack before any read marked %d err=%v", acked, err)
	}
	if err := r.MarkInboxDelivered("scout", []int64{stored.ID}); err != nil {
		t.Fatal(err)
	}
	if acked, err := r.AckInbox("scout", stored.ID); err != nil || acked != 1 {
		t.Fatalf("acked=%d err=%v after the upgrade", acked, err)
	}
	if _, _, err := r.BindRemoteIdentity("scout", "", ""); err != nil {
		t.Fatalf("remote binding after the upgrade: %v", err)
	}
}
