package registry

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestAnExpiredEventSubscriptionFreesItsPlaceAtOnce(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.AddAgentIdentity("kat"); err != nil {
		t.Fatal(err)
	}
	remote, _, err := r.BindRemoteIdentity("kat", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if remote, err = r.RemoteIdentityNamed("kat"); err != nil {
		t.Fatal(err)
	}
	epoch, err := r.RemoteEpoch()
	if err != nil {
		t.Fatal(err)
	}
	subscribe := func(n int, refreshBefore time.Time) error {
		_, err := r.SubscribeEvent(EventSubscribe{ID: fmt.Sprintf("sub_%d", n), IdentityID: remote.IdentityID, TokenHash: remote.TokenHash, Epoch: epoch, Name: "inbox.unread", Args: "{}", URL: fmt.Sprintf("https://example.com/%d", n), Secret: "whsec_x", RefreshBefore: refreshBefore, Verified: true, Limit: 8})
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
