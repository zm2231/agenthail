package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

// Runs under host.drain, after the held session sends.
func (d *Daemon) drainHostHeldActions(ctx context.Context, host *pairedHost) {
	held, err := d.Registry.HostHeldActions(host.record.ID)
	if err != nil {
		d.logRuntimeError("host "+host.record.Name+" held actions", err)
		return
	}
	if len(held) == 0 {
		return
	}
	defer d.refreshHostQueueCounts()
	for _, item := range held {
		if state, _ := host.status(); ctx.Err() != nil || state != hostStateConnected {
			return
		}
		d.replayHostHeldAction(ctx, host, item)
	}
}

func (d *Daemon) replayHostHeldAction(ctx context.Context, host *pairedHost, item registry.HostHeldAction) {
	inflightAt, claimed, err := d.Registry.ClaimHostHeldAction(item.ID)
	if err != nil || !claimed {
		if err != nil {
			d.logRuntimeError("host "+host.record.Name+" held action", err)
		}
		return
	}
	unconfirmed := func() {
		if time.Since(inflightAt) > d.federation.patience {
			d.settleHostHeldAction(host, item, registry.HostActionDead, fmt.Sprintf("host %s has not confirmed %s", host.record.Name, item.Label))
		}
	}
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/actions", nil, item.Request, http.Header{actionIdempotencyHeader: []string{item.Key}})
	if err != nil {
		var notSent hosts.NotSentError
		if errors.As(err, &notSent) && item.Status == registry.HostActionPending {
			if releaseErr := d.Registry.ReleaseHostHeldAction(item.ID); releaseErr != nil {
				d.logRuntimeError("host "+host.record.Name+" held action", releaseErr)
			}
			return
		}
		unconfirmed()
		return
	}
	answer, err := readHostAnswer(host, response)
	if err != nil {
		unconfirmed()
		return
	}
	var reply channelDeliverAnswer
	_ = json.Unmarshal(answer.payload, &reply)
	switch {
	case answer.status == http.StatusAccepted:
		unconfirmed()
	case answer.status == http.StatusOK || answer.status == http.StatusConflict && reply.Error.Code == "conflict":
		d.settleHostHeldAction(host, item, registry.HostActionDone, string(answer.payload))
	default:
		message := reply.Error.Message
		if message == "" {
			message = fmt.Sprintf("answered %d", answer.status)
		}
		d.settleHostHeldAction(host, item, registry.HostActionDead, fmt.Sprintf("host %s refused %s: %s", host.record.Name, item.Label, message))
	}
}

func (d *Daemon) settleHostHeldAction(host *pairedHost, item registry.HostHeldAction, status, outcome string) {
	if err := d.Registry.SettleHostHeldAction(item.ID, status, outcome); err != nil {
		d.logRuntimeError("host "+host.record.Name+" held action", err)
		return
	}
	entry := registry.HistoryEntry{Kind: "channel", Message: item.Label + " on " + host.record.Name, Result: "delivered"}
	if status == registry.HostActionDead {
		entry.Result, entry.Error = "failed", outcome
	}
	if err := d.Registry.RecordHistory(entry); err != nil {
		d.logRuntimeError("host "+host.record.Name+" held action", err)
	}
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
}
