package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const (
	connectionDropWindow   = 24 * time.Hour
	connectionSteadyLosses = 3
	phoneStreamIdleTimeout = 25 * time.Second
	streamCloseNearDrop    = 30 * time.Second

	dropCauseQuiet     = "daemon went quiet"
	dropCauseStopped   = "daemon stopped"
	dropCauseTransit   = "lost in transit"
	dropCauseHalfOpen  = "half-open"
	dropCauseReset     = "reset"
	dropCauseNoRecord  = "no daemon record"
	dropCauseUnclear   = "unclear"
	dropNetworkUnknown = "unknown network"
)

type connectionHealth struct {
	Devices  []deviceConnection `json:"devices"`
	Warnings []string           `json:"warnings"`
}

type deviceConnection struct {
	DeviceID string                   `json:"deviceId"`
	Name     string                   `json:"name"`
	Recent   int                      `json:"recent"`
	Held     int                      `json:"held"`
	Offline  int                      `json:"offline"`
	Relayed  int                      `json:"relayed"`
	Causes   []dropCauseCount         `json:"causes"`
	Last     *registry.ConnectionDrop `json:"last,omitempty"`
}

type dropCauseCount struct {
	Cause       string `json:"cause"`
	Network     string `json:"network"`
	TailnetPath string `json:"tailnetPath,omitempty"`
	Count       int    `json:"count"`
}

func connectionHealthOf(r *registry.Registry, now time.Time) (connectionHealth, error) {
	health := connectionHealth{Devices: []deviceConnection{}, Warnings: []string{}}
	histories, err := r.ConnectionDropHistories()
	if err != nil {
		return health, err
	}
	self, err := r.HostIdentity()
	if err != nil {
		return health, err
	}
	names, err := computerNames(r)
	if err != nil {
		return health, err
	}
	for _, history := range histories {
		last := history.Drops[0]
		last.Host, last.Via = named(names, last.Host), named(names, last.Via)
		device := deviceConnection{DeviceID: history.DeviceID, Name: history.Name, Causes: []dropCauseCount{}, Last: &last}
		streams, err := r.DeviceStreams(history.DeviceID, "events", now.Add(-connectionDropWindow-streamCloseNearDrop), now)
		if err != nil {
			return health, err
		}
		causes := map[dropCauseCount]int{}
		for _, drop := range history.Drops {
			if now.Sub(drop.StartedAt) > connectionDropWindow {
				continue
			}
			if drop.Host == self.ID && drop.Via == "" && strings.HasPrefix(drop.Path, "stream") {
				cause, path := streamDropCause(drop, streams)
				network := drop.Network
				if network == "" {
					network = dropNetworkUnknown
				}
				causes[dropCauseCount{Cause: cause, Network: network, TailnetPath: path}]++
			}
			device.Recent++
			if drop.Shown == "held" {
				device.Held++
			} else {
				device.Offline++
			}
			if drop.Via != "" {
				device.Relayed++
			}
		}
		for key, count := range causes {
			key.Count = count
			device.Causes = append(device.Causes, key)
		}
		sort.Slice(device.Causes, func(i, j int) bool {
			a, b := device.Causes[i], device.Causes[j]
			if a.Count != b.Count {
				return a.Count > b.Count
			}
			return a.Cause+a.Network+a.TailnetPath < b.Cause+b.Network+b.TailnetPath
		})
		health.Devices = append(health.Devices, device)
		if quiet := quietStreams(device.Causes); quiet > 0 {
			health.Warnings = append(health.Warnings, fmt.Sprintf("%d of %s's stream drops came after this computer sent it nothing for over %d s; that is an Agenthail bug, not the network", quiet, history.Name, int(phoneStreamIdleTimeout.Seconds())))
		}
		if lostInSteadyUse(history.Drops) {
			health.Warnings = append(health.Warnings, fmt.Sprintf("%s showed a computer offline %d times in a row while in use, not just after returning to the app", history.Name, connectionSteadyLosses))
		}
	}
	return health, nil
}

func streamDropCause(drop registry.ConnectionDrop, streams []registry.DeviceStream) (string, string) {
	at := drop.StartedAt
	var stream *registry.DeviceStream
	for i, candidate := range streams {
		if candidate.OpenedAt.After(at) || candidate.ClosedAt.Before(at.Add(-streamCloseNearDrop)) {
			continue
		}
		if stream == nil || candidate.OpenedAt.After(stream.OpenedAt) {
			stream = &streams[i]
		}
	}
	if stream == nil {
		return dropCauseNoRecord, ""
	}
	lastFlush := stream.LastFlushAt
	if lastFlush.IsZero() {
		lastFlush = stream.OpenedAt
	}
	closedNear := stream.ClosedAt.Sub(at).Abs() <= streamCloseNearDrop
	switch {
	case closedNear && stream.Ended == registry.StreamEndedDaemonStopped:
		return dropCauseStopped, stream.TailnetPath
	case closedNear && stream.Ended == registry.StreamEndedWriteFailed:
		return dropCauseReset, stream.TailnetPath
	case stream.ClosedAt.Sub(at) > streamCloseNearDrop && lastFlush.After(at):
		return dropCauseHalfOpen, stream.TailnetPath
	case at.Sub(lastFlush) > phoneStreamIdleTimeout:
		return dropCauseQuiet, stream.TailnetPath
	case closedNear && (stream.Ended == registry.StreamEndedClientClosed || stream.Ended == registry.StreamEndedWriteDeadline):
		return dropCauseTransit, stream.TailnetPath
	}
	return dropCauseUnclear, stream.TailnetPath
}

func quietStreams(causes []dropCauseCount) int {
	quiet := 0
	for _, cause := range causes {
		if cause.Cause == dropCauseQuiet {
			quiet += cause.Count
		}
	}
	return quiet
}

func describeCauses(causes []dropCauseCount) string {
	parts := make([]string, 0, len(causes))
	for _, cause := range causes {
		part := fmt.Sprintf("%d %s on %s", cause.Count, cause.Cause, cause.Network)
		if cause.TailnetPath != "" {
			part += " (" + strings.Replace(cause.TailnetPath, "relay:", "relay ", 1) + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func computerNames(r *registry.Registry) (map[string]string, error) {
	names := map[string]string{}
	self, err := r.HostIdentity()
	if err != nil {
		return nil, err
	}
	names[self.ID] = "this computer"
	paired, err := r.ListHosts()
	if err != nil {
		return nil, err
	}
	for _, host := range paired {
		names[host.ID] = host.Name
	}
	return names, nil
}

func named(names map[string]string, id string) string {
	if name, ok := names[id]; ok {
		return name
	}
	return id
}

func lostInSteadyUse(drops []registry.ConnectionDrop) bool {
	if len(drops) < connectionSteadyLosses {
		return false
	}
	for _, drop := range drops[:connectionSteadyLosses] {
		if drop.Shown != "offline" || drop.SinceForegroundMs != nil || drop.WentBackground {
			return false
		}
	}
	return true
}

func describeDrop(drop registry.ConnectionDrop) string {
	parts := []string{drop.StartedAt.Local().Format("Jan 2 15:04"), drop.Path}
	if drop.Via != "" {
		parts[1] += fmt.Sprintf(" through %s to %s", drop.Via, drop.Host)
	} else {
		parts[1] += " to " + drop.Host + " directly"
	}
	failure := drop.FirstError
	if drop.ElapsedMs != nil {
		failure += fmt.Sprintf(" after %.1f s", float64(*drop.ElapsedMs)/1000)
	}
	parts = append(parts, failure)
	if drop.Network != "" {
		network := "on " + drop.Network
		if drop.VPN {
			network += " via vpn"
		}
		parts = append(parts, network)
	}
	if drop.SinceForegroundMs != nil {
		parts = append(parts, fmt.Sprintf("%.0f s after returning to the app", float64(*drop.SinceForegroundMs)/1000))
	}
	back := fmt.Sprintf("back after %s", drop.RecoveredAt.Sub(drop.StartedAt).Round(time.Second))
	if drop.RecoveredBy != "" {
		back += " by " + drop.RecoveredBy
	}
	if drop.WentBackground {
		back += " (the app was in the background meanwhile)"
	}
	return strings.Join(append(parts, back), ", ")
}

func (h connectionHealth) String() string {
	if len(h.Devices) == 0 {
		return "connection: no phone has reported a dropped connection"
	}
	lines := []string{"connection: drops phones reported (information, not a failure)"}
	for _, device := range h.Devices {
		line := fmt.Sprintf("  %s (%s): %d drops in 24 h (%d held, %d shown offline", device.Name, shortID(device.DeviceID), device.Recent, device.Held, device.Offline)
		if device.Relayed > 0 {
			line += fmt.Sprintf(", %d through another computer", device.Relayed)
		}
		line += ")"
		if device.Last != nil {
			line += "; last " + describeDrop(*device.Last)
		}
		lines = append(lines, line)
		if len(device.Causes) > 0 {
			lines = append(lines, "    stream drops as this computer saw them through Tailscale: "+describeCauses(device.Causes))
		}
	}
	for _, warning := range h.Warnings {
		lines = append(lines, "  warning: "+warning)
	}
	return strings.Join(lines, "\n")
}
