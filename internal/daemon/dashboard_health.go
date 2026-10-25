package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) recordSurfaceHealth(ctx context.Context, adapter surface.Surface, listErr error) dashboardSurface {
	entry := d.dashboardSurfaceHealth(ctx, adapter, listErr)
	d.surfaceHealthMu.Lock()
	defer d.surfaceHealthMu.Unlock()
	if d.surfaceHealth == nil {
		d.surfaceHealth = map[surface.SurfaceKind]dashboardSurface{}
	}
	d.surfaceHealth[adapter.Name()] = entry
	return entry
}

func (d *Daemon) observedSurfaceHealth(kind surface.SurfaceKind) (dashboardSurface, bool) {
	d.surfaceHealthMu.Lock()
	defer d.surfaceHealthMu.Unlock()
	entry, found := d.surfaceHealth[kind]
	return entry, found
}

func (d *Daemon) dashboardSurfaceHealth(ctx context.Context, adapter surface.Surface, listErr error) dashboardSurface {
	entry := dashboardSurface{Name: string(adapter.Name()), Connected: listErr == nil, Health: "healthy", Capabilities: adapter.Capabilities()}
	if signin.IsOff(listErr) {
		entry.Health = "signin_off"
		entry.HealthDetail = listErr.Error()
		return entry
	}
	if signin.IsUnavailable(listErr) {
		entry.Health = "signin_unavailable"
		entry.HealthDetail = listErr.Error()
		return entry
	}
	if listErr != nil {
		entry.Error = listErr.Error()
		entry.Health = "unavailable"
		entry.HealthDetail = listErr.Error()
	}
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if checker, ok := adapter.(surface.HealthChecker); ok {
		if err := checker.Health(healthCtx); signin.IsUnavailable(err) {
			entry.Health = "signin_unavailable"
			entry.HealthDetail = err.Error()
		} else if err != nil {
			if entry.Connected {
				entry.Health = "degraded"
			}
			entry.HealthDetail = err.Error()
		}
	}
	if provider, ok := adapter.(surface.RuntimeStatusProvider); ok {
		runtimeStatus := provider.RuntimeStatus(healthCtx)
		if runtimeStatus.Name != "" {
			entry.Runtime = &runtimeStatus
			if !runtimeStatus.Reachable || !runtimeStatus.Durable {
				if entry.Health == "healthy" {
					entry.Health = "degraded"
				}
				if entry.HealthDetail == "" {
					entry.HealthDetail = runtimeStatus.Detail
				}
			}
			if entry.Health == "healthy" && entry.HealthDetail == "" {
				messages := make([]string, 0, len(runtimeStatus.Notes))
				for _, note := range runtimeStatus.Notes {
					messages = append(messages, note.Message)
				}
				entry.HealthDetail = strings.Join(messages, "; ")
			}
			switch runtimeStatus.Problem {
			case surface.RuntimeBridgeUnavailable:
				entry.RepairAction = "codex-launch"
				entry.RepairLabel = "Launch Codex through Agenthail"
			case surface.RuntimeStopped:
				entry.RepairAction = "runtime-ensure"
				entry.RepairLabel = "Start Codex app-server daemon"
			}
		}
	}
	return entry
}
