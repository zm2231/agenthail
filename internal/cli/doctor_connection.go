package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const (
	connectionDropWindow   = 24 * time.Hour
	connectionSteadyLosses = 3
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
	Last     *registry.ConnectionDrop `json:"last,omitempty"`
}

func connectionHealthOf(r *registry.Registry, now time.Time) (connectionHealth, error) {
	health := connectionHealth{Devices: []deviceConnection{}, Warnings: []string{}}
	histories, err := r.ConnectionDropHistories()
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
		device := deviceConnection{DeviceID: history.DeviceID, Name: history.Name, Last: &last}
		for _, drop := range history.Drops {
			if now.Sub(drop.StartedAt) > connectionDropWindow {
				continue
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
		health.Devices = append(health.Devices, device)
		if lostInSteadyUse(history.Drops) {
			health.Warnings = append(health.Warnings, fmt.Sprintf("%s showed a computer offline %d times in a row while in use, not just after returning to the app", history.Name, connectionSteadyLosses))
		}
	}
	return health, nil
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
	}
	for _, warning := range h.Warnings {
		lines = append(lines, "  warning: "+warning)
	}
	return strings.Join(lines, "\n")
}
