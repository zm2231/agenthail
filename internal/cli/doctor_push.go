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

func pushHealthOf(r *registry.Registry) (pushHealth, error) {
	health := pushHealth{RelayConfigured: daemon.PushRelayURL() != "", Devices: []registry.PushStatus{}, Problems: []string{}}
	devices, err := r.PushStatuses()
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
	}
	return health, nil
}

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
		lines = append(lines, line)
	}
	for _, problem := range h.Problems {
		lines = append(lines, "  problem: "+problem)
	}
	return strings.Join(lines, "\n")
}
