package registry

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// BrowserPushSubscription is where a signed-in browser takes Web Push
// messages. The endpoint is a capability URL: anyone holding it can send to
// that browser, so it is never logged or listed.
type BrowserPushSubscription struct {
	DeviceID string
	Endpoint string
	P256DH   string
	Auth     string
}

// SaveBrowserPushSubscription records the subscription of an active browser,
// replacing the one it had. Revoking or forgetting the browser removes it.
func (r *Registry) SaveBrowserPushSubscription(subscription BrowserPushSubscription) error {
	deviceID := strings.TrimSpace(subscription.DeviceID)
	endpoint := strings.TrimSpace(subscription.Endpoint)
	p256dh := strings.TrimSpace(subscription.P256DH)
	auth := strings.TrimSpace(subscription.Auth)
	if deviceID == "" || endpoint == "" || p256dh == "" || auth == "" {
		return fmt.Errorf("device id, endpoint, and keys are required")
	}
	if len(endpoint) > 2048 || len(p256dh) > 128 || len(auth) > 64 {
		return fmt.Errorf("push subscription is too large")
	}
	if parsed, err := url.Parse(endpoint); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("push endpoint must be an https URL")
	}
	result, err := r.db.Exec(`INSERT INTO browser_push_subscriptions (device_id,endpoint,p256dh,auth,updated_at)
		SELECT id,?,?,?,? FROM paired_devices WHERE id=? AND browser=1 AND revoked_at=''
		ON CONFLICT(device_id) DO UPDATE SET endpoint=excluded.endpoint,p256dh=excluded.p256dh,auth=excluded.auth,updated_at=excluded.updated_at`, endpoint, p256dh, auth, formatTimestamp(time.Now()), deviceID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return fmt.Errorf("active browser not found")
	}
	return nil
}

func (r *Registry) RemoveBrowserPushSubscription(deviceID string) error {
	_, err := r.db.Exec(`DELETE FROM browser_push_subscriptions WHERE device_id=?`, strings.TrimSpace(deviceID))
	return err
}

// RemoveBrowserPushEndpoint removes a subscription only while it still has
// the endpoint a push service refused, so one the browser replaced meanwhile
// stays.
func (r *Registry) RemoveBrowserPushEndpoint(deviceID, endpoint string) error {
	_, err := r.db.Exec(`DELETE FROM browser_push_subscriptions WHERE device_id=? AND endpoint=?`, strings.TrimSpace(deviceID), endpoint)
	return err
}

func (r *Registry) HasBrowserPushSubscription(deviceID string) (bool, error) {
	var found bool
	err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM browser_push_subscriptions WHERE device_id=?)`, strings.TrimSpace(deviceID)).Scan(&found)
	return found, err
}

// BrowserPushSubscriptions lists the subscriptions of browsers still signed
// in. A browser past BrowserIdleLimit is left out even before it is pruned.
func (r *Registry) BrowserPushSubscriptions() ([]BrowserPushSubscription, error) {
	cutoff := formatTimestamp(time.Now().Add(-BrowserIdleLimit))
	rows, err := r.read.Query(`SELECT b.device_id,b.endpoint,b.p256dh,b.auth
		FROM browser_push_subscriptions b JOIN paired_devices d ON d.id=b.device_id
		WHERE d.revoked_at='' AND d.browser=1
			AND datetime(CASE WHEN d.last_seen_at<>'' THEN d.last_seen_at ELSE d.created_at END)>=datetime(?)
		ORDER BY b.updated_at DESC`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subscriptions := []BrowserPushSubscription{}
	for rows.Next() {
		var subscription BrowserPushSubscription
		if err := rows.Scan(&subscription.DeviceID, &subscription.Endpoint, &subscription.P256DH, &subscription.Auth); err != nil {
			return nil, err
		}
		subscriptions = append(subscriptions, subscription)
	}
	return subscriptions, rows.Err()
}
