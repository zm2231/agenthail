package registry

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func eventSubscriber(t *testing.T) (*Registry, func(n int) (EventSubscription, error)) {
	t.Helper()
	r := openTestRegistry(t)
	identity, err := r.AddAgentIdentity("kat")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.BindRemoteIdentity("kat", "", ""); err != nil {
		t.Fatal(err)
	}
	return r, func(n int) (EventSubscription, error) {
		remote, err := r.RemoteIdentityNamed("kat")
		if err != nil {
			return EventSubscription{}, err
		}
		epoch, err := r.RemoteEpoch()
		if err != nil {
			return EventSubscription{}, err
		}
		return r.SubscribeEvent(EventSubscribe{ID: fmt.Sprintf("sub_%d", n), IdentityID: identity.SessionID, TokenHash: remote.TokenHash, Epoch: epoch, Name: "inbox.unread", Args: "{}", URL: fmt.Sprintf("https://example.com/%d", n), Secret: "whsec_x", Verified: true, Limit: 8})
	}
}

func TestADeliveryBegunBeforeARefreshCannotUndoIt(t *testing.T) {
	r, subscribe := eventSubscriber(t)
	old, err := subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RotateRemoteIdentity("kat"); err != nil {
		t.Fatal(err)
	}
	fresh, err := subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID != old.ID || fresh.Generation == old.Generation {
		t.Fatalf("old=%+v fresh=%+v, want the same id under a new generation", old, fresh)
	}
	if err := r.DeferEvent(old.ID, old.Generation, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := r.EventFailed(old.ID, old.Generation, 3); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.DropEventSubscription(old.ID, old.Generation); err != nil {
		t.Fatal(err)
	}
	sub, live, err := r.LiveEventSubscription(fresh.ID)
	if err != nil || !live || sub.Failures != 0 || !sub.SuspendedAt.IsZero() || sub.Generation != fresh.Generation {
		t.Fatalf("the fresh subscription after stale writes: %+v live=%v err=%v", sub, live, err)
	}
	if _, _, err := r.StoreInboxMessage(InboxMessage{Identity: sub.IdentityID, UUID: "m", Message: "m"}); err != nil {
		t.Fatal(err)
	}
	due, err := r.DueEvents(time.Now(), time.Hour)
	if err != nil || len(due) != 1 || due[0].ID != fresh.ID {
		t.Fatalf("due=%+v err=%v, want the fresh subscription despite a stale deferral", due, err)
	}
}

func TestAnUpgradeKeepsAnEventSubscriptionThatWasSetToExpire(t *testing.T) {
	r, subscribe := eventSubscriber(t)
	sub, err := subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`ALTER TABLE mcp_event_subscriptions ADD COLUMN refresh_before_ms INTEGER NOT NULL DEFAULT 0;
		UPDATE mcp_event_subscriptions SET refresh_before_ms=?;
		DELETE FROM schema_migrations WHERE name='2026-11-21-event-subscriptions-never-expire'`, time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	path := r.path
	r.Close()
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if kept, live, err := upgraded.LiveEventSubscription(sub.ID); err != nil || !live || kept.Generation != sub.Generation {
		t.Fatalf("after the upgrade: %+v live=%v err=%v", kept, live, err)
	}
	tx, err := upgraded.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	column, err := columnExists(tx, "mcp_event_subscriptions", "refresh_before_ms")
	tx.Rollback()
	if err != nil || column {
		t.Fatalf("refresh_before_ms after the upgrade: present=%v err=%v", column, err)
	}
	if _, _, err := upgraded.StoreInboxMessage(InboxMessage{Identity: sub.IdentityID, UUID: "m", Message: "m"}); err != nil {
		t.Fatal(err)
	}
	if due, err := upgraded.DueEvents(time.Now(), time.Hour); err != nil || len(due) != 1 || due[0].ID != sub.ID {
		t.Fatalf("due=%+v err=%v, want the subscription that was set to expire", due, err)
	}
	upgraded.Close()
	withMigrations(t)
	var refused *UnknownMigrationsError
	if _, err := Open(path); !errors.As(err, &refused) || !strings.Contains(err.Error(), "2026-11-21-event-subscriptions-never-expire") {
		t.Fatalf("a build without the migration opened the upgraded registry: %v", err)
	}
}

func TestASubscriptionToldAfterTheDueListWasReadIsNoLongerDue(t *testing.T) {
	r, subscribe := eventSubscriber(t)
	sub, err := subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, uuid := range []string{"a", "b"} {
		if _, _, err := r.StoreInboxMessage(InboxMessage{Identity: sub.IdentityID, UUID: uuid, Message: uuid}); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := r.DueEvents(time.Now(), time.Hour)
	if err != nil || len(listed) != 1 || listed[0].Unread != 2 {
		t.Fatalf("due=%+v err=%v", listed, err)
	}
	if err := r.EventDelivered(sub.ID, sub.Generation, listed[0].Newest); err != nil {
		t.Fatal(err)
	}
	if due, still, err := r.DueEvent(sub.ID, time.Now(), time.Hour); err != nil || still {
		t.Fatalf("after the delivery: %+v due=%v err=%v, want it told", due, still, err)
	}
	if _, _, err := r.StoreInboxMessage(InboxMessage{Identity: sub.IdentityID, UUID: "c", Message: "c"}); err != nil {
		t.Fatal(err)
	}
	if due, still, err := r.DueEvent(sub.ID, time.Now(), time.Hour); err != nil || !still || due.Unread != 3 {
		t.Fatalf("after new mail: %+v due=%v err=%v, want 3 unread", due, still, err)
	}
}
