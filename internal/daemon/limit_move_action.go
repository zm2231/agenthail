package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) releaseMovedLimitStop(sessionID string) {
	stop, found, err := d.Registry.LimitResumeFor(sessionID)
	if err != nil || !found || stop.State != registry.LimitResumeWaiting {
		return
	}
	d.logLimitResume(d.releaseLimitStop(stop, limitWaiting, true, time.Now()))
}

// rootProxy is the proxy the named or given root runs through, "" for a
// directly signed-in or unknown one.
func (v proxyView) rootProxy(choice string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path, err := surface.ResolveClaudeConfigRoot(home, choice)
	if err != nil {
		return ""
	}
	root, _ := surface.ClaudeConfigRootFor(v.roots, path)
	return root.Proxy
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
	now := time.Now()
	view := d.readProxyView(now)
	if root == "" {
		if offer := d.cachedLimitMoveOffers(view, now)[sessionID]; offer != nil {
			root = offer.Root
		}
	}
	if root == "" {
		return actionResult{}, actionFailed(http.StatusConflict, "move_refused", "no other Claude account has room for this session now")
	}
	if proxy := view.rootProxy(root); proxy != "" && !view.room(proxy, d.sessionIsFable(sessionID), now) {
		return actionResult{}, actionFailed(http.StatusConflict, "move_refused", fmt.Sprintf("the %s proxy that Claude config directory %s runs through has no account with room for this session now", proxy, root))
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
