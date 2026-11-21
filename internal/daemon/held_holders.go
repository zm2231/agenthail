package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// Nothing else retries a held message until its target goes idle, so a terminal that closed would stay on its ask.
func (d *Daemon) dropExitedHolders(now time.Time) {
	held, err := d.Registry.HeldSharedMessages()
	if err != nil {
		d.log.Printf("read messages held for several terminals: %s", err)
		return
	}
	checker, ok := d.adapters().ForSurface(surface.KindClaude).(surface.HolderChecker)
	if len(held) == 0 || !ok {
		return
	}
	for _, message := range held {
		var left []surface.Holder
		for _, holder := range message.Holders {
			if checker.StillHolds(message.SessionID, holder) {
				left = append(left, holder)
			}
		}
		changed, err := d.Registry.KeepHeldHolders(message, left, now)
		if err != nil {
			d.log.Printf("recheck the terminals holding queue item %d: %s", message.ID, err)
		} else if changed {
			d.log.Printf("queue item %d: %d of the %d terminals holding %s are still open", message.ID, len(left), len(message.Holders), d.resolveDisplay(message.SessionID))
		}
	}
}

// A terminal closing replaces the ask, so a pick from the replaced one counts for a process that was already running when it was raised.
func (d *Daemon) pinAfterHoldersChanged(ask registry.Ask, ref registry.DeliveryRef, pid int) (actionResult, error) {
	checker, _ := d.adapters().ForSurface(surface.KindClaude).(surface.HolderChecker)
	err := d.Registry.PinHeldMessage(ref.QueueID, pid, func(holder surface.Holder) bool {
		return checker != nil && checker.StartedBefore(holder, ask.CreatedAt)
	})
	if errors.Is(err, registry.ErrMessageNotHeld) {
		return actionResult{}, settledRequest(ask)
	}
	if err != nil {
		return actionResult{}, actionFailed(http.StatusConflict, "conflict", fmt.Sprintf("%s; the message stays with the terminals still open", err))
	}
	d.syncDeliveryAsks()
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": ask.ID, "outcome": "answered", "choice": registry.TerminalOption(pid)}}), nil
}

func replacedTerminalPick(ask registry.Ask, response surface.Response) bool {
	if ask.Origin != registry.AskOriginDelivery || ask.State != registry.AskWithdrawn || len(response.Choices) != 1 {
		return false
	}
	ref, ok := registry.ParseDeliveryRef(ask.Ref)
	_, terminal := registry.TerminalChoice(response.Choices[0])
	return ok && terminal && ref.Kind == registry.DeliveryShared
}
