package registry

import (
	"testing"
	"time"
)

func pairTestDevice(t *testing.T, r *Registry, browser bool) (Device, string) {
	t.Helper()
	scopes := []string{"read", "control"}
	if browser {
		scopes = BrowserScopes
	}
	pairing, err := r.CreateDevicePairing("Device", scopes, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if browser {
		device, token, err := r.CompleteBrowserPairing(pairing.Secret)
		if err != nil {
			t.Fatal(err)
		}
		return device, token
	}
	device, token, err := r.CompleteDevicePairing(pairing.Secret, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return device, token
}

func browserSubscription(deviceID, endpoint string) BrowserPushSubscription {
	return BrowserPushSubscription{DeviceID: deviceID, Endpoint: endpoint, P256DH: "p256dh", Auth: "auth"}
}

func TestBrowserPushSubscriptionsBelongToActiveBrowsers(t *testing.T) {
	r := openTestRegistry(t)
	browser, _ := pairTestDevice(t, r, true)
	phone, _ := pairTestDevice(t, r, false)
	if err := r.SaveBrowserPushSubscription(browserSubscription(phone.ID, "https://push.example/phone")); err == nil {
		t.Fatal("a phone took a browser subscription")
	}
	for _, endpoint := range []string{"http://push.example/plain", "https:///no-host", "push.example/relative"} {
		if err := r.SaveBrowserPushSubscription(browserSubscription(browser.ID, endpoint)); err == nil {
			t.Fatalf("%s was accepted", endpoint)
		}
	}
	if err := r.SaveBrowserPushSubscription(browserSubscription(browser.ID, "https://push.example/first")); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveBrowserPushSubscription(browserSubscription(browser.ID, "https://push.example/second")); err != nil {
		t.Fatal(err)
	}
	subscriptions, err := r.BrowserPushSubscriptions()
	if err != nil || len(subscriptions) != 1 || subscriptions[0].Endpoint != "https://push.example/second" {
		t.Fatalf("subscriptions=%+v err=%v", subscriptions, err)
	}
	devices, err := r.ListDevices(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range devices {
		if device.PushEnabled != (device.ID == browser.ID) {
			t.Fatalf("device %s pushEnabled=%v", device.ID, device.PushEnabled)
		}
	}
	if err := r.RemoveBrowserPushEndpoint(browser.ID, "https://push.example/first"); err != nil {
		t.Fatal(err)
	}
	if found, err := r.HasBrowserPushSubscription(browser.ID); err != nil || !found {
		t.Fatalf("removing a replaced endpoint removed the current one: found=%v err=%v", found, err)
	}
	if err := r.RevokeDevice(browser.ID); err != nil {
		t.Fatal(err)
	}
	if found, err := r.HasBrowserPushSubscription(browser.ID); err != nil || found {
		t.Fatalf("a revoked browser kept its subscription: found=%v err=%v", found, err)
	}
	if err := r.SaveBrowserPushSubscription(browserSubscription(browser.ID, "https://push.example/third")); err == nil {
		t.Fatal("a revoked browser subscribed")
	}
}

func TestIdleBrowsersLoseTheirSubscription(t *testing.T) {
	r := openTestRegistry(t)
	browser, _ := pairTestDevice(t, r, true)
	if err := r.SaveBrowserPushSubscription(browserSubscription(browser.ID, "https://push.example/idle")); err != nil {
		t.Fatal(err)
	}
	longAgo := time.Now().UTC().Add(-BrowserIdleLimit - time.Hour).Format(time.RFC3339Nano)
	if _, err := r.db.Exec(`UPDATE paired_devices SET last_seen_at=? WHERE id=?`, longAgo, browser.ID); err != nil {
		t.Fatal(err)
	}
	if subscriptions, err := r.BrowserPushSubscriptions(); err != nil || len(subscriptions) != 0 {
		t.Fatalf("an idle browser is still notified before pruning: %+v err=%v", subscriptions, err)
	}
	if _, err := r.ListDevices(true); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := r.db.QueryRow(`SELECT count(*) FROM browser_push_subscriptions WHERE device_id=?`, browser.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("pruning the browser left %d subscription rows: %v", rows, err)
	}
}
