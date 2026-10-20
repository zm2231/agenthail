package daemon

import (
	"bytes"
	"context"
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
// request the device fetches from this Mac to offer its buttons; what the
// request asks never goes through the relay.
type mobileNotification struct {
	Title, Message, SessionID, TurnID, EventType, CollapseID, RequestID string
}

func (d *Daemon) notifyPairedDevices(ctx context.Context, notification mobileNotification) {
	targets, err := d.Registry.DevicePushTargets()
	if err != nil {
		d.log.Printf("load device push targets: %s", err)
		return
	}
	var wait sync.WaitGroup
	concurrency := make(chan struct{}, 4)
	for _, target := range targets {
		target := target
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case concurrency <- struct{}{}:
				defer func() { <-concurrency }()
			case <-ctx.Done():
				return
			}
			if err := sendDevicePushWithRetry(ctx, target, notification); err != nil {
				d.log.Printf("mobile notification %s: %s", target.DeviceID, err)
				if isTerminalPushRelayError(err) {
					if removeErr := d.Registry.RemoveDevicePushTarget(target.DeviceID); removeErr != nil {
						d.log.Printf("remove rejected mobile notification target %s: %s", target.DeviceID, removeErr)
					}
				}
			}
		}()
	}
	wait.Wait()
}

func sendDevicePushWithRetry(ctx context.Context, target registry.DevicePushTarget, notification mobileNotification) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt*attempt) * devicePushRetryDelay
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if err := sendDevicePush(ctx, target, notification); err != nil {
			lastErr = err
			var relayErr *pushRelayError
			if errors.As(err, &relayErr) && isTerminalPushRelayError(err) {
				return err
			}
			continue
		}
		return nil
	}
	return lastErr
}

func isTerminalPushRelayError(err error) bool {
	var relayErr *pushRelayError
	return errors.As(err, &relayErr) && relayErr.status >= http.StatusBadRequest && relayErr.status < http.StatusInternalServerError && relayErr.status != http.StatusTooManyRequests
}

func sendDevicePush(ctx context.Context, target registry.DevicePushTarget, notification mobileNotification) error {
	relayURL := PushRelayURL()
	if relayURL == "" {
		return errors.New("push relay is not configured")
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
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, relayURL+"/v1/send", bytes.NewReader(payload))
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
