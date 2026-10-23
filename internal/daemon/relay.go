package daemon

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	maxRelayText = 24000
	maxRelayHops = 8
)

func relayTargetInactive(session *surface.Session) bool {
	return session.Status == surface.StatusOffline
}

// claudeProcessExited reports a Claude session whose process is gone. It stays
// a delivery target, since a send resumes it, but produces no turns to observe.
func claudeProcessExited(session *surface.Session) bool {
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

func relayable(route registry.RouteRow, event registry.RelayEvent) bool {
	return event.Message != "" && event.RelayHops < maxRelayHops && matchPattern(route.Pattern, event.Message)
}

// sessionObservation reads a session's turn state at most once and shares the
// reading between its relays and the scan that observes it.
type sessionObservation struct {
	read        func(context.Context) (*surface.TurnObservation, error)
	done        bool
	observation *surface.TurnObservation
	err         error
}

var errNoAdapter = errors.New("no adapter for this session")

func observeOnce(adapter surface.Surface, session *surface.Session, read func(context.Context, surface.Surface, *surface.Session) (*surface.TurnObservation, error)) *sessionObservation {
	return &sessionObservation{read: func(ctx context.Context) (*surface.TurnObservation, error) {
		if adapter == nil {
			return nil, errNoAdapter
		}
		return read(ctx, adapter, session)
	}}
}

func observeAdapter(ctx context.Context, adapter surface.Surface, session *surface.Session) (*surface.TurnObservation, error) {
	return adapter.Observe(ctx, session)
}

func (o *sessionObservation) get(ctx context.Context) (*surface.TurnObservation, error) {
	if !o.done {
		readCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		o.observation, o.err = o.read(readCtx)
		cancel()
		o.done = true
	}
	return o.observation, o.err
}

// backgroundHold keeps a finished turn from relaying while the source still
// runs background work it started; ambient tasks do not count. A source that
// closed has no work left. When its state cannot be read, the last stored
// state decides.
func backgroundHold(ctx context.Context, observed *sessionObservation, from *surface.Session) error {
	running := surface.BackgroundRunning(from.BackgroundTasks)
	observation, err := observed.get(ctx)
	if err == nil {
		running = observation != nil && observation.Status != surface.StatusOffline && surface.BackgroundRunning(observation.BackgroundTasks)
	}
	if !running {
		return nil
	}
	if err != nil {
		return fmt.Errorf("waiting for background work to finish; its state is unavailable: %w", err)
	}
	return fmt.Errorf("waiting for background work to finish")
}

func (d *Daemon) syncRelays(ctx context.Context, adapter surface.Surface, session *surface.Session) {
	d.syncRelaysObserved(ctx, adapter, session, observeOnce(adapter, session, observeAdapter))
}

func (d *Daemon) syncRelaysObserved(ctx context.Context, adapter surface.Surface, session *surface.Session, observed *sessionObservation) {
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
	d.consumeRelays(ctx, adapter, session, active, captureErr, observed)
}

func (d *Daemon) consumeRelays(ctx context.Context, adapter surface.Surface, from *surface.Session, routes []registry.RouteRow, captureErr error, observed *sessionObservation) {
	for _, route := range routes {
		if !route.Active || route.FromSession != from.ID {
			continue
		}
		events, err := d.Registry.PendingRelayEvents(route)
		goalKey := route.LastGoalKey
		if err == nil && route.Handoff == registry.HandoffGoalCompleted && len(events) > 0 {
			if captureErr != nil {
				err = captureErr
			} else if !surface.EffectiveCapabilities(adapter, from).Goal {
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
					observation, observeErr := observed.get(ctx)
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
		if err == nil && route.Handoff == registry.HandoffTurnFinished && len(events) > 0 {
			err = backgroundHold(ctx, observed, from)
		}
		if err == nil {
			last := -1
			for index, event := range events {
				if relayable(route, event) {
					last = index
				}
			}
			for index, event := range events {
				payload, busy := "", ""
				if relayable(route, event) && (!route.Once || index == last) {
					target, targetErr := d.Registry.Session(route.ToSession)
					if targetErr != nil {
						err = targetErr
						break
					}
					if !d.hostOwned(target.ID) && surface.IsReadOnlySession(target) {
						err = fmt.Errorf("destination is read-only: %s", surface.ReadOnlySessionReason(target))
						break
					}
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
