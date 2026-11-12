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

func goalReplyIndex(events []registry.RelayEvent, since time.Time) int {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Message != "" && !events[index].CompletedAt.Before(since) {
			return index
		}
	}
	return len(events) - 1
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

// relayScan is one scan's relay reads: each adapter that reads many sessions'
// completions in one pass gives the scan a single reader. A nil scan reads
// through the adapter itself.
type relayScan map[surface.CompletionScanner]surface.CompletionReader

func (s relayScan) reader(adapter surface.Surface) (surface.CompletionReader, bool) {
	if scanner, ok := adapter.(surface.CompletionScanner); ok && s != nil {
		if s[scanner] == nil {
			s[scanner] = scanner.CompletionScan()
		}
		return s[scanner], true
	}
	reader, ok := adapter.(surface.CompletionReader)
	return reader, ok
}

func (d *Daemon) syncRelays(ctx context.Context, adapter surface.Surface, session *surface.Session) {
	d.syncRelaysObserved(ctx, nil, adapter, session, observeOnce(adapter, session, observeAdapter))
}

func (d *Daemon) syncRelaysObserved(ctx context.Context, scan relayScan, adapter surface.Surface, session *surface.Session, observed *sessionObservation) {
	active, err := d.activeRelaysFrom(session.ID)
	if err != nil {
		d.log.Printf("list relays: %s", err)
		return
	}
	if len(active) == 0 {
		return
	}
	d.consumeRelays(ctx, adapter, session, active, d.captureRelays(ctx, scan, adapter, session), observed)
}

func (d *Daemon) activeRelaysFrom(session string) ([]registry.RouteRow, error) {
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		return nil, err
	}
	var active []registry.RouteRow
	for _, route := range routes {
		if route.Active && route.FromSession == session {
			active = append(active, route)
		}
	}
	return active, nil
}

func (d *Daemon) captureRelays(ctx context.Context, scan relayScan, adapter surface.Surface, session *surface.Session) error {
	reader, ok := scan.reader(adapter)
	if !ok {
		return fmt.Errorf("source does not support replayable completions")
	}
	cursor, err := d.Registry.RelaySourceCursor(session.ID)
	if err != nil {
		return err
	}
	readCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	batch, err := reader.ReadCompletions(readCtx, session, cursor)
	cancel()
	if err != nil {
		return err
	}
	err = d.Registry.CaptureRelayCompletions(session.ID, cursor, batch)
	if err == nil && len(batch.Events) > 0 {
		d.publishEvent("state.changed", session.ID, map[string]string{"source": "relay-capture"})
	}
	if errors.Is(err, registry.ErrRelayCursorMoved) {
		return nil
	}
	return err
}

// forwardRelaysBeforeMove forwards what the held session's relays have not:
// the target reads its completions only from after the move.
func (d *Daemon) forwardRelaysBeforeMove(ctx context.Context, adapter surface.Surface, session *surface.Session) error {
	active, err := d.activeRelaysFrom(session.ID)
	if err != nil || len(active) == 0 {
		return err
	}
	if err := d.captureRelays(ctx, nil, adapter, session); err != nil {
		return fmt.Errorf("read its completions for its relays: %w", err)
	}
	d.consumeRelays(ctx, adapter, session, active, nil, observeOnce(adapter, session, observeAdapter))
	return nil
}

func (d *Daemon) consumeRelays(ctx context.Context, adapter surface.Surface, from *surface.Session, routes []registry.RouteRow, captureErr error, observed *sessionObservation) {
	for _, route := range routes {
		if !route.Active || route.FromSession != from.ID {
			continue
		}
		events, err := d.Registry.PendingRelayEvents(route)
		goalKey := route.LastGoalKey
		var stopped *surface.GoalState
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
				} else if goal == nil || goal.Status != surface.GoalStatusComplete && !goalStopped(goal.Status) || goal.CreatedAt.IsZero() {
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
					goalKey = goalKeyOf(goal)
					if err == nil {
						switch {
						case goalKey == route.LastGoalKey && goalStopped(goal.Status):
							err = fmt.Errorf("the goal is %s; waiting for it to resume", goal.Status)
						case goalKey == route.LastGoalKey:
							err = fmt.Errorf("waiting for a new completed goal")
						default:
							events = events[goalReplyIndex(events, goal.CreatedAt):]
							if goalStopped(goal.Status) {
								stopped = goal
							}
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
					if stopped != nil {
						text = goalStopNote(d.answerTarget(from.ID), stopped) + "\n\n" + text
					}
					payload = fmt.Sprintf("[agenthail relay hops=%d id=%d turn=%s source=%s] %s", event.RelayHops+1, route.ID, event.CompletionID, d.resolveDisplay(from.ID), text)
				}
				if stopped != nil {
					_, err = d.Registry.ConsumeRelayReport(route, event, payload, busy, goalKey)
				} else {
					_, err = d.Registry.ConsumeRelay(route, event, payload, busy, goalKey)
				}
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
