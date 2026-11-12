package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
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
	kind := "channel"
	if !strings.HasPrefix(item.Label, "#") {
		kind = "sent"
	}
	entry := registry.HistoryEntry{Kind: kind, Message: item.Label + " on " + host.record.Name, Result: "delivered"}
	if status == registry.HostActionDead {
		entry.Result, entry.Error = "failed", outcome
	}
	if err := d.Registry.RecordHistory(entry); err != nil {
		d.logRuntimeError("host "+host.record.Name+" held action", err)
	}
	if status == registry.HostActionDead {
		d.tellHeldActionSender(host, item, outcome)
	}
	d.invalidateDashboard()
}

func (d *Daemon) tellHeldActionSender(host *pairedHost, item registry.HostHeldAction, outcome string) {
	var request struct {
		Source string `json:"sourceSessionId"`
	}
	if json.Unmarshal(item.Request, &request) != nil {
		return
	}
	owner, source, qualified := hosts.Split(request.Source)
	if !qualified || owner != d.hostIdentity().ID || source == surface.SystemSessionID {
		return
	}
	if _, err := d.Registry.Session(source); err != nil {
		return
	}
	message := fmt.Sprintf("[agenthail] Your message to %s, held while host %s was unreachable, was not delivered: %s", item.Label, host.record.Name, outcome)
	if _, err := d.sendNotice(noticeRecipient{sessionID: source}, message, "held-action-failed:"+item.Key); err != nil {
		d.logRuntimeError("host "+host.record.Name+" held action", err)
	}
}

func hostActionKey(base, hostID string) string {
	key := base + ":" + hostID
	if len(key) <= registry.ActionReceiptKeyLimit {
		return key
	}
	sum := sha256.Sum256([]byte(base))
	return "h-" + hex.EncodeToString(sum[:16]) + ":" + hostID
}
