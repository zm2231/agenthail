package daemon

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const limitAutoMoveTimeout = 2 * time.Minute

func (d *Daemon) releaseMovedLimitStop(sessionID string) {
	stop, found, err := d.Registry.LimitResumeFor(sessionID)
	if err != nil || !found || stop.State != registry.LimitResumeWaiting {
		return
	}
	d.logLimitResume(d.releaseLimitStop(stop, limitWaiting, true, time.Now()))
}

func (d *Daemon) doLimitMove(ctx context.Context, sessionID, root string, stopTerminal bool) (actionResult, error) {
	claimed, moving := d.claimLimitMove(sessionID, "")
	if moving {
		return actionResult{}, actionFailed(http.StatusConflict, "move_busy", "this session is already moving to another Claude account")
	}
	if !claimed {
		return actionResult{}, actionFailed(http.StatusConflict, "move_refused", "this session is not waiting on a usage limit")
	}
	defer d.unclaimLimitMove(sessionID)
	if root == "" {
		if offer := d.limitMoveOffer(sessionID, time.Now()); offer != nil {
			root = offer.Root
		}
	}
	if root == "" {
		return actionResult{}, actionFailed(http.StatusConflict, "move_refused", "no other Claude account has room for this session now")
	}
	result, err := d.moveSessionToRoot(context.WithoutCancel(ctx), sessionMoveRequest{SessionID: sessionID, Root: root, StopTerminal: stopTerminal})
	var refused MoveRefusedError
	var busy *surface.AdoptBusyError
	var consent *surface.AdoptConsentError
	switch {
	case err == nil:
		return actionOK(map[string]any{"ok": true, "result": result}), nil
	case errors.As(err, &busy):
		return actionResult{}, &actionError{Status: http.StatusConflict, Code: "move_busy", Message: err.Error(), Extra: map[string]any{"busy": busy.Reason}}
	case errors.As(err, &consent):
		return actionResult{}, actionFailed(http.StatusConflict, "move_needs_consent", err.Error())
	case errors.As(err, &refused):
		return actionResult{}, actionFailed(http.StatusConflict, "move_refused", err.Error())
	default:
		return actionResult{}, actionFailed(http.StatusBadGateway, "move_failed", err.Error())
	}
}

func (d *Daemon) autoMoveLimitStops(ctx context.Context, now time.Time) {
	stops, err := d.Registry.LimitResumes()
	if err != nil {
		return
	}
	moves := d.limitResumes.moves
	moves.prune(stops, now)
	if !d.claudeAccountPolicy().AutoMove {
		return
	}
	offers := d.cachedLimitMoveOffers(now)
	if len(offers) == 0 {
		return
	}
	sort.SliceStable(stops, func(i, j int) bool { return stops[i].RefusedAt.Before(stops[j].RefusedAt) })
	proxied := map[string]bool{}
	for _, root := range d.currentClaudeConfigRoots() {
		proxied[root.Root] = root.Proxy != ""
	}
	for _, stop := range stops {
		offer := offers[stop.SessionID]
		if offer == nil || stop.State != registry.LimitResumeWaiting || proxied[stop.AccountKey] {
			continue
		}
		key := limitMoveKey(stop)
		moves.mu.Lock()
		last, spaced := moves.movedAt[offer.Root]
		if moves.tried[key] || spaced && now.Sub(last) < limitResumeSpacing {
			moves.mu.Unlock()
			continue
		}
		moves.tried[key], moves.movedAt[offer.Root] = true, now
		moves.mu.Unlock()
		if claimed, _ := d.claimLimitMove(stop.SessionID, stop.TurnID); !claimed {
			continue
		}
		moveCtx, cancel := context.WithTimeout(ctx, limitAutoMoveTimeout)
		_, err := d.moveSessionToRoot(moveCtx, sessionMoveRequest{SessionID: stop.SessionID, Root: offer.Root})
		cancel()
		d.unclaimLimitMove(stop.SessionID)
		if err != nil {
			d.log.Printf("move %s to account %s after its usage limit: %s", d.resolveDisplay(stop.SessionID), offer.Root, err)
		}
		moves.forget()
		return
	}
}
