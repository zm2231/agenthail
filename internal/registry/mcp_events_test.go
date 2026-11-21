package registry

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func eventSubscriber(t *testing.T) (*Registry, func(n int, refreshBefore time.Time) (EventSubscription, error)) {
	t.Helper()
	r := openTestRegistry(t)
	identity, err := r.AddAgentIdentity("kat")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.BindRemoteIdentity("kat", "", ""); err != nil {
		t.Fatal(err)
	}
	return r, func(n int, refreshBefore time.Time) (EventSubscription, error) {
		remote, err := r.RemoteIdentityNamed("kat")
		if err != nil {
			return EventSubscription{}, err
		}
		epoch, err := r.RemoteEpoch()
		if err != nil {
			return EventSubscription{}, err
		}
		return r.SubscribeEvent(EventSubscribe{ID: fmt.Sprintf("sub_%d", n), IdentityID: identity.SessionID, TokenHash: remote.TokenHash, Epoch: epoch, Name: "inbox.unread", Args: "{}", URL: fmt.Sprintf("https://example.com/%d", n), Secret: "whsec_x", RefreshBefore: refreshBefore, Verified: true, Limit: 8})
	}
}

func TestAnExpiredEventSubscriptionFreesItsPlaceAtOnce(t *testing.T) {
	_, subscribeAt := eventSubscriber(t)
	subscribe := func(n int, refreshBefore time.Time) error {
		_, err := subscribeAt(n, refreshBefore)
		return err
	}
	for n := range 8 {
		if err := subscribe(n, time.Now().Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := subscribe(8, time.Time{}); err != nil {
		t.Fatalf("eight expired subscriptions held the place: %v", err)
	}
	for n := 9; n < 16; n++ {
		if err := subscribe(n, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := subscribe(16, time.Time{}); !errors.Is(err, ErrEventSubscriptionLimit) {
		t.Fatalf("a ninth live subscription: %v", err)
	}
}

func TestADeliveryBegunBeforeARefreshCannotUndoIt(t *testing.T) {
	r, subscribe := eventSubscriber(t)
	old, err := subscribe(1, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.RotateRemoteIdentity("kat"); err != nil {
		t.Fatal(err)
	}
	fresh, err := subscribe(1, time.Time{})
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
	sub, live, err := r.LiveEventSubscription(fresh.ID, time.Now())
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
