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

func (d *Daemon) limitMoveAction(w http.ResponseWriter, r *http.Request, sessionID, root string, stopTerminal bool) {
	claimed, moving := d.claimLimitMove(sessionID, "")
	if moving {
		writeAPIError(w, http.StatusConflict, "move_busy", "this session is already moving to another Claude account")
		return
	}
	if !claimed {
		writeAPIError(w, http.StatusConflict, "move_refused", "this session is not waiting on a usage limit")
		return
	}
	defer d.unclaimLimitMove(sessionID)
	if root == "" {
		if offer := d.limitMoveOffer(sessionID, time.Now()); offer != nil {
			root = offer.Root
		}
	}
	if root == "" {
		writeAPIError(w, http.StatusConflict, "move_refused", "no other Claude account has room for this session now")
		return
	}
	result, err := d.moveSessionToRoot(context.WithoutCancel(r.Context()), sessionMoveRequest{SessionID: sessionID, Root: root, StopTerminal: stopTerminal})
	var refused MoveRefusedError
	var busy *surface.AdoptBusyError
	var consent *surface.AdoptConsentError
	switch {
	case err == nil:
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
	case errors.As(err, &busy):
		writeAPIErrorWith(w, http.StatusConflict, "move_busy", err.Error(), map[string]any{"busy": busy.Reason})
	case errors.As(err, &consent):
		writeAPIError(w, http.StatusConflict, "move_needs_consent", err.Error())
	case errors.As(err, &refused):
		writeAPIError(w, http.StatusConflict, "move_refused", err.Error())
	default:
		writeAPIError(w, http.StatusBadGateway, "move_failed", err.Error())
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
