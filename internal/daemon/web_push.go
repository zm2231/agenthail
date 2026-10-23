package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/zm2231/agenthail/internal/registry"
)

// webPushSubscriber is the contact push services see in the VAPID token;
// Apple's service requires a mailto: or https: one.
const webPushSubscriber = "https://github.com/zm2231/agenthail"

// webPushQuestionTTL is how long a push service keeps an undelivered
// question for a browser that is offline; past it the question is likely
// answered or stale.
const webPushQuestionTTL = 15 * 60

var webPushHTTPClient webpush.HTTPClient = &http.Client{Timeout: 12 * time.Second}

type webPushKeys struct {
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
}

func WebPushKeyPath() string {
	return filepath.Join(filepath.Dir(PidFilePath()), "web-push-vapid.json")
}

var webPushKeyMu sync.Mutex

// webPushKey is this host's VAPID key pair, made on first use. Every browser
// subscription is bound to its public key, so replacing the file ends every
// subscription.
func webPushKey() (webPushKeys, error) {
	webPushKeyMu.Lock()
	defer webPushKeyMu.Unlock()
	path := WebPushKeyPath()
	data, err := os.ReadFile(path)
	if err == nil {
		var keys webPushKeys
		if json.Unmarshal(data, &keys) == nil && keys.PublicKey != "" && keys.PrivateKey != "" {
			return keys, nil
		}
		return webPushKeys{}, fmt.Errorf("read web push key: %s is not a key pair", path)
	}
	if !os.IsNotExist(err) {
		return webPushKeys{}, fmt.Errorf("read web push key: %w", err)
	}
	var keys webPushKeys
	keys.PrivateKey, keys.PublicKey, err = webpush.GenerateVAPIDKeys()
	if err != nil {
		return webPushKeys{}, fmt.Errorf("generate web push key: %w", err)
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		return webPushKeys{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return webPushKeys{}, fmt.Errorf("write web push key: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".web-push-vapid-*")
	if err != nil {
		return webPushKeys{}, fmt.Errorf("write web push key: %w", err)
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return webPushKeys{}, fmt.Errorf("write web push key: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return webPushKeys{}, fmt.Errorf("write web push key: %w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return webPushKeys{}, fmt.Errorf("write web push key: %w", err)
	}
	return keys, nil
}

// webPushMessage is what the dashboard's service worker receives. A withdraw
// names the notification to close; every other message is one to show.
type webPushMessage struct {
	Type       string `json:"type"`
	Title      string `json:"title,omitempty"`
	Message    string `json:"message,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	TurnID     string `json:"turnId,omitempty"`
	EventType  string `json:"eventType,omitempty"`
	CollapseID string `json:"collapseId,omitempty"`
	RequestID  string `json:"requestId,omitempty"`
}

type webPushError struct{ status int }

func (e *webPushError) Error() string {
	return fmt.Sprintf("push service returned %d", e.status)
}

// isGoneWebPushError is a push service saying the subscription no longer
// exists, the one refusal that ends it.
func isGoneWebPushError(err error) bool {
	var pushErr *webPushError
	return errors.As(err, &pushErr) && (pushErr.status == http.StatusNotFound || pushErr.status == http.StatusGone)
}

func (d *Daemon) notifyBrowsers(ctx context.Context, notification mobileNotification) {
	message := webPushMessage{
		Type:       "notify",
		Title:      boundedNotificationText(notification.Title, 120),
		Message:    boundedNotificationText(notification.Message, 1200),
		SessionID:  boundedNotificationText(notification.SessionID, 240),
		TurnID:     boundedNotificationText(notification.TurnID, 240),
		EventType:  boundedNotificationText(notification.EventType, 80),
		CollapseID: boundedNotificationText(notification.CollapseID, 64),
		RequestID:  boundedNotificationText(notification.RequestID, 240),
	}
	urgency := webpush.UrgencyNormal
	if notification.EventType == "session.waiting" {
		urgency = webpush.UrgencyHigh
	}
	d.sendToBrowsers(ctx, message, urgency, "browser notification")
}

// withdrawFromBrowsers closes the notification each browser shows under
// collapseID. A message with the same topic also replaces an undelivered
// one, so a browser that was offline never shows the answered question.
func (d *Daemon) withdrawFromBrowsers(ctx context.Context, collapseID string) {
	d.sendToBrowsers(ctx, webPushMessage{Type: "withdraw", CollapseID: boundedNotificationText(collapseID, 64)}, webpush.UrgencyNormal, "withdraw browser notification")
}

func (d *Daemon) sendToBrowsers(ctx context.Context, message webPushMessage, urgency webpush.Urgency, what string) {
	subscriptions, err := d.Registry.BrowserPushSubscriptions()
	if err != nil {
		d.log.Printf("load browser push subscriptions: %s", err)
		return
	}
	if len(subscriptions) == 0 {
		return
	}
	keys, err := webPushKey()
	if err != nil {
		d.log.Printf("%s: %s", what, err)
		return
	}
	payload, err := json.Marshal(message)
	if err != nil {
		d.log.Printf("%s: %s", what, err)
		return
	}
	var wait sync.WaitGroup
	concurrency := make(chan struct{}, 4)
	for _, subscription := range subscriptions {
		subscription := subscription
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case concurrency <- struct{}{}:
				defer func() { <-concurrency }()
			case <-ctx.Done():
				return
			}
			err := sendWebPush(ctx, keys, subscription, payload, webPushTopic(message.CollapseID), urgency)
			if err == nil {
				return
			}
			d.log.Printf("%s %s: %s", what, subscription.DeviceID, err)
			if isGoneWebPushError(err) {
				if removeErr := d.Registry.RemoveBrowserPushEndpoint(subscription.DeviceID, subscription.Endpoint); removeErr != nil {
					d.log.Printf("remove refused browser push subscription %s: %s", subscription.DeviceID, removeErr)
				}
			}
		}()
	}
	wait.Wait()
}

// webPushTopic is the Topic header for a collapse key. A topic is at most 32
// URL-safe base64 characters, which 24 bytes of the key's hash fill.
func webPushTopic(collapseID string) string {
	if collapseID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(collapseID))
	return base64.RawURLEncoding.EncodeToString(sum[:24])
}

// sendWebPush delivers one encrypted message. Its errors never carry the
// endpoint, which is a capability URL.
func sendWebPush(ctx context.Context, keys webPushKeys, subscription registry.BrowserPushSubscription, payload []byte, topic string, urgency webpush.Urgency) error {
	response, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
		Endpoint: subscription.Endpoint,
		Keys:     webpush.Keys{P256dh: subscription.P256DH, Auth: subscription.Auth},
	}, &webpush.Options{
		HTTPClient:      webPushHTTPClient,
		Subscriber:      webPushSubscriber,
		Topic:           topic,
		TTL:             webPushQuestionTTL,
		Urgency:         urgency,
		VAPIDPublicKey:  keys.PublicKey,
		VAPIDPrivateKey: keys.PrivateKey,
	})
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return fmt.Errorf("send to push service: %w", urlErr.Err)
		}
		return fmt.Errorf("send to push service: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	return &webPushError{status: response.StatusCode}
}
