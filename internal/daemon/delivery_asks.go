package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) syncDeliveryAsks() {
	if err := d.Registry.SyncDeliveryAsks(time.Now()); err != nil {
		d.log.Printf("sync delivery asks: %s", err)
	}
	d.tellDeliveryAsks()
}

func (d *Daemon) tellDeliveryAsks() {
	d.deliveryAskMu.Lock()
	defer d.deliveryAskMu.Unlock()
	untold, err := d.Registry.UntoldDeliveryAsks()
	if err != nil {
		d.log.Printf("read untold delivery asks: %s", err)
		return
	}
	for _, ask := range untold {
		if ask.RoutedTo == registry.AskToPerson {
			d.announceAsk(ask)
		} else if _, err := d.sendNotice(noticeRecipient{sessionID: ask.RoutedTo, role: "sender"}, d.askNoticeFor(ask, ask.RoutedTo), askNoticeKey(ask.ID, ask.RoutedTo)); err != nil {
			d.log.Printf("tell %s its message was not delivered: %s", d.resolveDisplay(ask.RoutedTo), err)
			d.escalateAsk(ask, "")
		}
		if err := d.Registry.MarkAskAnnounced(ask.ID, time.Now()); err != nil {
			d.log.Printf("mark %s told: %s", ask.ID, err)
		}
		d.publishAsk(ask.SessionID)
	}
	if len(untold) > 0 {
		if err := d.catalog.flushCommitted(); err != nil {
			d.log.Printf("publish delivery asks: %s", err)
		}
	}
}

func deliveryChoice(ask registry.Ask, response surface.Response) (registry.AskOption, bool) {
	if len(ask.Questions) == 0 || len(response.Choices) != 1 {
		return registry.AskOption{}, false
	}
	for _, option := range ask.Questions[0].Options {
		if option.ID == response.Choices[0] {
			return option, true
		}
	}
	return registry.AskOption{}, false
}

func deliveryOptionIDs(ask registry.Ask) string {
	if len(ask.Questions) == 0 {
		return ""
	}
	ids := make([]string, 0, len(ask.Questions[0].Options))
	for _, option := range ask.Questions[0].Options {
		ids = append(ids, option.ID)
	}
	return strings.Join(ids, ", ")
}

func (d *Daemon) answerDelivery(ctx context.Context, ask registry.Ask, response surface.Response, by, via string) (actionResult, error) {
	if _, err := askRequest(ask).Resolve(response); err != nil {
		return actionResult{}, invalidRequest(err.Error())
	}
	option, ok := deliveryChoice(ask, response)
	if !ok {
		return actionResult{}, invalidRequest("answer with one choice of: " + deliveryOptionIDs(ask))
	}
	ref, ok := registry.ParseDeliveryRef(ask.Ref)
	if !ok {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", "unreadable delivery ask "+ask.ID)
	}
	claimed, err := d.Registry.ClaimAsk(ask.ID, registry.AskAnswer{Answers: map[string]string{ask.Questions[0].Question: option.Label}}, by, via, time.Now())
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if !claimed {
		return actionResult{}, staleRequest()
	}
	defer d.publishAsk(ask.SessionID)
	if err := d.applyDelivery(ctx, ref, option.ID); err != nil {
		if releaseErr := d.Registry.ReleaseAsk(ask.ID); releaseErr != nil {
			d.log.Printf("reopen %s: %s", ask.ID, releaseErr)
		}
		return actionResult{}, actionFailed(http.StatusConflict, "conflict", err.Error())
	}
	if err := d.Registry.RecordAskApplied(ask.ID, "applied", "", time.Now()); err != nil {
		d.log.Printf("record the answer to %s: %s", ask.ID, err)
	}
	d.syncDeliveryAsks()
	return actionOK(map[string]any{"ok": true, "result": map[string]any{"requestId": ask.ID, "outcome": "answered", "choice": option.ID}}), nil
}

func (d *Daemon) applyDelivery(ctx context.Context, ref registry.DeliveryRef, choice string) error {
	switch choice {
	case registry.DeliveryDismiss:
		return nil
	case registry.DeliveryDelete:
		err := d.Registry.DeleteFailedDelivery(ref.IntentID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("delivery %d is gone", ref.IntentID)
		}
		return err
	case registry.DeliveryCancel:
		_, err := d.Registry.CancelMessage(ref.QueueID)
		return err
	case registry.DeliveryRetry:
		queueID := ref.QueueID
		if ref.IntentID != 0 {
			intent, err := d.Registry.DeliveryIntent(ref.IntentID)
			if err != nil {
				return err
			}
			queueID = intent.QueueID
		}
		return d.retryDeadMessage(ctx, queueID)
	}
	return fmt.Errorf("%s is not a delivery answer", choice)
}

func (d *Daemon) retryDeadMessage(ctx context.Context, queueID int64) error {
	item, err := d.Registry.QueueItem(queueID, d.adapters())
	if err != nil {
		return err
	}
	target, err := d.Registry.Session(item.SessionID)
	if err != nil {
		return err
	}
	if owner, ownerErr := d.Registry.SessionHost(target.ID); ownerErr != nil || owner == "" {
		adapter, err := d.adapters().For(target)
		if err != nil {
			return errors.New("the target's surface is not configured")
		}
		if err := d.ensureDashboardWritable(ctx, adapter, target); err != nil {
			return err
		}
	}
	return d.Registry.RetryMessage(queueID)
}

// sendFailed answers a refused send or steer; when the daemon kept the message as a failed delivery, the reply names its ask so the client can answer it.
func (d *Daemon) sendFailed(status int, code string, err error) error {
	failure := &actionError{Status: status, Code: code, Message: err.Error()}
	var failed delivery.FailedDeliveryError
	if !errors.As(err, &failed) {
		return failure
	}
	ask, found, lookupErr := d.Registry.DeliveryAskOf(failed.DeliveryID)
	if lookupErr != nil {
		d.log.Printf("read the ask of delivery %d: %s", failed.DeliveryID, lookupErr)
	}
	if found {
		failure.Extra = map[string]any{"askId": ask.ID}
	}
	return failure
}
