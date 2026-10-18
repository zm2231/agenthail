package daemon

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	maxRelayText = 24000
	maxRelayHops = 8
)

func relayTargetInactive(session *surface.Session) bool {
	if session.Status == surface.StatusOffline {
		return true
	}
	if session.Surface != surface.KindClaude || session.PID <= 0 {
		return false
	}
	err := syscall.Kill(session.PID, 0)
	return err != nil && !errors.Is(err, syscall.EPERM)
}

func matchPattern(pattern, text string) bool {
	if pattern == "" || pattern == ".*" {
		return true
	}
	match, err := regexp.Compile(pattern)
	return err == nil && match.MatchString(text)
}

func (d *Daemon) syncRelays(ctx context.Context, adapter surface.Surface, session *surface.Session) {
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		d.log.Printf("list relays: %s", err)
		return
	}
	var active []registry.RouteRow
	for _, route := range routes {
		if route.Active && route.FromSession == session.ID {
			active = append(active, route)
		}
	}
	if len(active) == 0 {
		return
	}
	reader, ok := adapter.(surface.CompletionReader)
	captureErr := error(nil)
	if !ok {
		captureErr = fmt.Errorf("source does not support replayable completions")
	} else {
		cursor, err := d.Registry.RelaySourceCursor(session.ID)
		if err == nil {
			readCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
			batch, readErr := reader.ReadCompletions(readCtx, session, cursor)
			cancel()
			err = readErr
			if err == nil {
				err = d.Registry.CaptureRelayCompletions(session.ID, cursor, batch)
				if err == nil && len(batch.Events) > 0 {
					d.publishEvent("state.changed", session.ID, map[string]string{"source": "relay-capture"})
				}
				if errors.Is(err, registry.ErrRelayCursorMoved) {
					err = nil
				}
			}
		}
		captureErr = err
	}
	d.consumeRelays(ctx, adapter, session, active, captureErr)
}

func (d *Daemon) consumeRelays(ctx context.Context, adapter surface.Surface, from *surface.Session, routes []registry.RouteRow, captureErr error) {
	for _, route := range routes {
		if !route.Active || route.FromSession != from.ID {
			continue
		}
		events, err := d.Registry.PendingRelayEvents(route)
		goalKey := route.LastGoalKey
		if err == nil && route.Handoff == registry.HandoffGoalCompleted && len(events) > 0 {
			if captureErr != nil {
				err = captureErr
			} else if !surface.EffectiveCapabilities(from, adapter.Capabilities()).Goal {
				err = fmt.Errorf("source does not support goal-completed handoffs")
			} else {
				goalCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
				goal, goalErr := adapter.GoalGet(goalCtx, from)
				cancel()
				if goalErr != nil {
					err = fmt.Errorf("goal state unavailable: %w", goalErr)
				} else if goal == nil || goal.Status != surface.GoalStatusComplete || goal.CreatedAt.IsZero() {
					err = fmt.Errorf("waiting for explicit goal completion")
				} else {
					observeCtx, observeCancel := context.WithTimeout(ctx, surfaceOperationTimeout)
					observation, observeErr := adapter.Observe(observeCtx, from)
					observeCancel()
					if observeErr != nil {
						err = observeErr
					} else if observation == nil || observation.ActiveTurnID != "" || observation.CompletedTurnID != events[len(events)-1].CompletionID {
						err = fmt.Errorf("waiting for the goal's final turn to finish")
					}
					latest := events[len(events)-1]
					if err == nil && (latest.CompletedAt.IsZero() || latest.CompletedAt.Before(goal.CreatedAt)) {
						err = fmt.Errorf("waiting for a completion timestamp within this goal")
					}
					goalKey = goal.CreatedAt.UTC().Format(time.RFC3339Nano) + ":" + goal.Objective
					if err == nil {
						if goalKey == route.LastGoalKey {
							err = fmt.Errorf("waiting for a new completed goal")
						} else {
							events = events[len(events)-1:]
						}
					}
				}
			}
		}
		if err == nil {
			for _, event := range events {
				payload, busy := "", ""
				if event.Message != "" && event.RelayHops < maxRelayHops && matchPattern(route.Pattern, event.Message) {
					target, targetErr := d.Registry.Session(route.ToSession)
					if targetErr != nil {
						err = targetErr
						break
					}
					if surface.IsReadOnlySession(target) {
						err = fmt.Errorf("destination is read-only: %s", surface.ReadOnlySessionReason(target))
						break
					}
					policy, policyErr := deliverypolicy.Load()
					if policyErr != nil {
						err = policyErr
						break
					}
					busy = string(policy)
					text := event.Message
					if len(text) > maxRelayText {
						text = text[:maxRelayText] + "\n[relay text truncated by agenthail]"
					}
					payload = fmt.Sprintf("[agenthail relay hops=%d id=%d turn=%s source=%s] %s", event.RelayHops+1, route.ID, event.CompletionID, d.resolveDisplay(from.ID), text)
				}
				_, err = d.Registry.ConsumeRelay(route, event, payload, busy, goalKey)
				if err != nil {
					break
				}
				route.EventCursor = event.ID
				route.LastError = ""
				d.publishEvent("state.changed", from.ID, map[string]string{"source": "relay"})
				if payload != "" && route.Once {
					break
				}
			}
		}
		if err == nil {
			err = captureErr
		}
		message := ""
		if err != nil {
			message = err.Error()
		}
		if message == route.LastError {
			continue
		}
		if message != "" {
			d.log.Printf("relay %d: %s", route.ID, err)
		}
		if saveErr := d.Registry.SetRelayError(route.ID, message); saveErr != nil {
			d.log.Printf("record relay status %d: %s", route.ID, saveErr)
		} else {
			d.publishEvent("state.changed", from.ID, map[string]string{"source": "relay-status"})
		}
	}
}
