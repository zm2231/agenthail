package daemon

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

type pushAttempt struct {
	notification mobileNotification
	devices      int
	mu           sync.Mutex
	delivered    int
	failures     map[string]string
}

func newPushAttempt(notification mobileNotification, devices int) *pushAttempt {
	return &pushAttempt{notification: notification, devices: devices, failures: map[string]string{}}
}

func (a *pushAttempt) record(deviceID string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.delivered++
		return
	}
	a.failures[shortDeviceID(deviceID)] = err.Error()
}

func (a *pushAttempt) line(relayConfigured bool) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	request := a.notification.RequestID
	if request == "" {
		request = "-"
	}
	line := fmt.Sprintf("push %s", a.notification.EventType)
	if a.notification.SessionID != "" {
		line += fmt.Sprintf(" session=%s request=%s", a.notification.SessionID, request)
	}
	if a.notification.CollapseID != "" {
		line += " key=" + a.notification.CollapseID
	}
	line += fmt.Sprintf(" devices=%d delivered=%d failed=%d", a.devices, a.delivered, len(a.failures))
	if a.devices == 0 {
		line += " (no paired device takes notifications)"
	}
	if !relayConfigured {
		line += " (push relay is not configured)"
	}
	if len(a.failures) > 0 {
		devices := make([]string, 0, len(a.failures))
		for device := range a.failures {
			devices = append(devices, device)
		}
		sort.Strings(devices)
		errors := make([]string, 0, len(devices))
		for _, device := range devices {
			errors = append(errors, device+": "+a.failures[device])
		}
		line += " errors: " + strings.Join(errors, "; ")
	}
	return line
}

func shortDeviceID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
