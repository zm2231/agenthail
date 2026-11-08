package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

var bundledPushRelayURL string

var pushHTTPClient = &http.Client{Timeout: 12 * time.Second}

var devicePushRetryDelay = 250 * time.Millisecond

type pushRelayError struct {
	status int
	detail string
}

func (e *pushRelayError) Error() string {
	return fmt.Sprintf("relay returned %d: %s", e.status, e.detail)
}

func PushRelayURL() string {
	if value := strings.TrimSpace(os.Getenv("AGENTHAIL_PUSH_RELAY_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return strings.TrimRight(strings.TrimSpace(bundledPushRelayURL), "/")
}

// mobileNotification is one push to every paired device. CollapseID, when
// set, makes a repeat of the same notification replace the earlier one on the
// device instead of adding a second. RequestID, when set, names the open
// request the device fetches from this computer to show what it asks and offer
// any buttons; what the request asks never goes through the relay.
type mobileNotification struct {
	Title, Message, SessionID, TurnID, EventType, CollapseID, RequestID string
	// DedupeKey names a notification every computer paired to the phone may
	// send, so the relay delivers it once.
	DedupeKey string
	// PushID names one send to one device, which its receipt quotes back.
	PushID string
}

// notifyPairedDevices sends one notification to every paired phone, through
// the relay, and to every subscribed browser, directly.
func (d *Daemon) notifyPairedDevices(ctx context.Context, notification mobileNotification) {
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		d.notifyBrowsers(ctx, notification)
	}()
	defer wait.Wait()
	targets, err := d.Registry.DevicePushTargets()
	if err != nil {
		d.log.Printf("push %s: load device push targets: %s", notification.EventType, err)
		return
	}
	attempt := newPushAttempt(notification, len(targets))
	var sends sync.WaitGroup
	concurrency := make(chan struct{}, 4)
	for _, target := range targets {
		target := target
		sends.Add(1)
		go func() {
			defer sends.Done()
			var sendErr error
			receipt := false
			sent := notification
			sent.PushID = strings.ToLower(rand.Text())
			select {
			case concurrency <- struct{}{}:
				defer func() { <-concurrency }()
				if recordErr := d.Registry.RecordPushSent(target.DeviceID, sent.PushID, notification.EventType, time.Now()); recordErr != nil {
					d.log.Printf("record push sent %s: %s", target.DeviceID, recordErr)
				}
				receipt, sendErr = sendDevicePushWithRetry(ctx, target, sent)
				if settleErr := d.Registry.SettlePushSent(sent.PushID, receipt, time.Now()); settleErr != nil {
					d.log.Printf("settle push sent %s: %s", target.DeviceID, settleErr)
				}
			case <-ctx.Done():
				sendErr = fmt.Errorf("not sent: %w", ctx.Err())
			}
			attempt.record(target.DeviceID, sendErr)
			if recordErr := d.Registry.RecordPushOutcome(target.DeviceID, notification.EventType, time.Now(), sendErr); recordErr != nil {
				d.log.Printf("record push outcome %s: %s", target.DeviceID, recordErr)
			}
			if isTerminalPushRelayError(sendErr) {
				if removeErr := d.Registry.RemoveDevicePushTarget(target.DeviceID); removeErr != nil {
					d.log.Printf("remove rejected mobile notification target %s: %s", target.DeviceID, removeErr)
				}
			}
		}()
	}
	sends.Wait()
	d.log.Print(attempt.line(PushRelayURL() != ""))
}

func sendDevicePushWithRetry(ctx context.Context, target registry.DevicePushTarget, notification mobileNotification) (bool, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt*attempt) * devicePushRetryDelay
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		receipt, err := sendDevicePush(ctx, target, notification)
		if err != nil {
			lastErr = err
			var relayErr *pushRelayError
			if errors.As(err, &relayErr) && isTerminalPushRelayError(err) {
				return false, err
			}
			continue
		}
		return receipt, nil
	}
	return false, lastErr
}

func isTerminalPushRelayError(err error) bool {
	var relayErr *pushRelayError
	return errors.As(err, &relayErr) && relayErr.status >= http.StatusBadRequest && relayErr.status < http.StatusInternalServerError && relayErr.status != http.StatusTooManyRequests
}

// sendDevicePush reports whether the relay passed the push id on, so the
// device will send a receipt; a relay from before receipts does not.
func sendDevicePush(ctx context.Context, target registry.DevicePushTarget, notification mobileNotification) (bool, error) {
	relayURL := PushRelayURL()
	if relayURL == "" {
		return false, errors.New("push relay is not configured")
	}
	body := map[string]any{
		"installationId": target.InstallationID,
		"credential":     target.Credential,
		"title":          boundedNotificationText(notification.Title, 120),
		"message":        boundedNotificationText(notification.Message, 1200),
		"sessionId":      boundedNotificationText(notification.SessionID, 240),
		"turnId":         boundedNotificationText(notification.TurnID, 240),
		"eventType":      boundedNotificationText(notification.EventType, 80),
		"collapseId":     boundedNotificationText(notification.CollapseID, 64),
	}
	if notification.RequestID != "" {
		body["requestId"] = boundedNotificationText(notification.RequestID, 240)
	}
	if notification.PushID != "" {
		body["pushId"] = notification.PushID
	}
	if notification.DedupeKey != "" {
		body["dedupeKey"] = notification.DedupeKey
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL+"/v1/send", bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := pushHTTPClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		var sent struct {
			Receipt bool `json:"receipt"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&sent)
		return sent.Receipt && notification.PushID != "", nil
	}
	detail, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	return false, &pushRelayError{status: response.StatusCode, detail: strings.TrimSpace(string(detail))}
}

// withdrawFromPairedDevices asks every paired phone and subscribed browser to
// remove the notification it showed under collapseID. A relay that cannot
// withdraw is not a reason to stop notifying a phone, so no relay failure
// here removes one; a browser is removed only when its push service says the
// subscription is gone, as on a send.
func (d *Daemon) withdrawFromPairedDevices(ctx context.Context, collapseID string) {
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		d.withdrawFromBrowsers(ctx, collapseID)
	}()
	defer wait.Wait()
	targets, err := d.Registry.DevicePushTargets()
	if err != nil {
		d.log.Printf("load device push targets: %s", err)
		return
	}
	attempt := newPushAttempt(mobileNotification{EventType: "withdraw", CollapseID: collapseID}, len(targets))
	for _, target := range targets {
		attempt.record(target.DeviceID, sendDeviceWithdraw(ctx, target, collapseID))
	}
	d.log.Print(attempt.line(PushRelayURL() != ""))
}

func sendDeviceWithdraw(ctx context.Context, target registry.DevicePushTarget, collapseID string) error {
	relayURL := PushRelayURL()
	if relayURL == "" {
		return errors.New("push relay is not configured")
	}
	payload, err := json.Marshal(map[string]any{
		"installationId": target.InstallationID,
		"credential":     target.Credential,
		"collapseId":     boundedNotificationText(collapseID, 64),
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL+"/v1/withdraw", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := pushHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	detail, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	return &pushRelayError{status: response.StatusCode, detail: strings.TrimSpace(string(detail))}
}
