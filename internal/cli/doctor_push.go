package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
)

type pushHealth struct {
	RelayConfigured bool                  `json:"relayConfigured"`
	Devices         []registry.PushStatus `json:"devices"`
	Problems        []string              `json:"problems"`
}

func pushHealthOf(r *registry.Registry, now time.Time) (pushHealth, error) {
	health := pushHealth{RelayConfigured: daemon.PushRelayURL() != "", Devices: []registry.PushStatus{}, Problems: []string{}}
	devices, err := r.PushStatuses(now.Add(-pushArrivalGrace))
	if err != nil {
		return health, err
	}
	health.Devices = devices
	if len(devices) > 0 && !health.RelayConfigured {
		health.Problems = append(health.Problems, "no push relay is configured, so no paired device is notified")
	}
	for _, device := range devices {
		if failedSinceDelivery(device) {
			health.Problems = append(health.Problems, fmt.Sprintf("the last push to %s failed at %s: %s", device.Name, device.FailedAt, device.Error))
		}
		if device.Unarrived >= pushUnarrivedLimit {
			health.Problems = append(health.Problems, fmt.Sprintf("APNs accepted %d pushes to %s that never reached the phone: check its notification settings and Focus for Agenthail", device.Unarrived, device.Name))
		}
	}
	return health, nil
}

const (
	pushArrivalGrace   = 10 * time.Minute
	pushUnarrivedLimit = 2
)

func failedSinceDelivery(device registry.PushStatus) bool {
	failed, err := time.Parse(time.RFC3339Nano, device.FailedAt)
	if err != nil {
		return false
	}
	delivered, err := time.Parse(time.RFC3339Nano, device.SucceededAt)
	return err != nil || failed.After(delivered)
}

func (h pushHealth) String() string {
	var lines []string
	if len(h.Devices) == 0 {
		lines = append(lines, "push: no paired device takes notifications")
	} else {
		relay := "relay configured"
		if !h.RelayConfigured {
			relay = "no relay configured"
		}
		lines = append(lines, fmt.Sprintf("push: %s, %d device(s)", relay, len(h.Devices)))
	}
	for _, device := range h.Devices {
		line := fmt.Sprintf("  %s (%s): registered %s", device.Name, shortID(device.DeviceID), device.RegisteredAt)
		if !device.Enabled {
			line += ", disabled"
		}
		if device.SucceededAt == "" {
			line += ", no push delivered yet"
		} else {
			line += ", last delivered " + device.SucceededAt
		}
		if device.FailedAt != "" {
			line += fmt.Sprintf(", last error at %s: %s", device.FailedAt, device.Error)
		}
		if device.ReceivedAt != "" {
			line += ", last arrived on the phone " + device.ReceivedAt
			if device.Alerts != "" {
				line += " (alerts " + device.Alerts + ")"
			}
		}
		lines = append(lines, line)
	}
	if len(h.Devices) > 0 {
		lines = append(lines, "  delivered means APNs accepted the push; arrived means the phone received it; arrival is not display, since Focus can still silence it")
	}
	for _, problem := range h.Problems {
		lines = append(lines, "  problem: "+problem)
	}
	return strings.Join(lines, "\n")
}
