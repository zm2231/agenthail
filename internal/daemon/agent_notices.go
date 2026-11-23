package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/agentroles"
)

// agentNotices holds what to tell the person once an agent's action that
// the CLI runs has succeeded; the CLI names it back by token.
type agentNotices struct {
	mu      sync.Mutex
	pending map[string]agentNotice
}

type agentNotice struct {
	callerID string
	what     agentAction
	expires  time.Time
}

// For a steer, notSteered is what to say when the session's host delivered
// it as a message, and unknown what to say when that host did not say.
// target is set when the action was not known as the notice was held, so
// the action the CLI then ran words it.
type agentAction struct {
	done       string
	notSteered string
	unknown    string
	target     string
}

func (a agentAction) told(notSteered *bool, ran string) string {
	if a.target != "" && ran != "" {
		return remoteActionWords(ran, a.target).told(notSteered, "")
	}
	switch {
	case notSteered == nil && a.unknown != "":
		return a.unknown
	case notSteered != nil && *notSteered && a.notSteered != "":
		return a.notSteered
	}
	return a.done
}

// A caller holds at most agentNoticesPerCaller unredeemed notices; a
// command whose action fails never redeems its own, so the oldest goes.
const (
	agentNoticeLifetime   = 10 * time.Minute
	agentNoticesPerCaller = 16
)

func (n *agentNotices) hold(callerID string, what agentAction, now time.Time) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.pending == nil {
		n.pending = map[string]agentNotice{}
	}
	held, oldest := 0, ""
	for token, notice := range n.pending {
		switch {
		case now.After(notice.expires):
			delete(n.pending, token)
		case notice.callerID == callerID:
			held++
			if oldest == "" || notice.expires.Before(n.pending[oldest].expires) {
				oldest = token
			}
		}
	}
	if held >= agentNoticesPerCaller {
		delete(n.pending, oldest)
	}
	token := uuid.NewString()
	n.pending[token] = agentNotice{callerID: callerID, what: what, expires: now.Add(agentNoticeLifetime)}
	return token
}

func (n *agentNotices) take(token, callerID string, now time.Time) (agentAction, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	notice, found := n.pending[token]
	if !found || notice.callerID != callerID || now.After(notice.expires) {
		return agentAction{}, false
	}
	delete(n.pending, token)
	return notice.what, true
}

// notifies reports whether the decision a session's host applied to an
// action tells the person; a host too old to report one leaves it unset.
func notifies(decision string) bool {
	return decision != agentroles.Allow
}

func (d *Daemon) holdAgentNotice(caller *mcpCaller, what string) string {
	if what == "" {
		return ""
	}
	return d.holdAgentAction(caller, agentAction{done: what})
}

func (d *Daemon) holdAgentAction(caller *mcpCaller, what agentAction) string {
	if what.done == "" {
		return ""
	}
	return d.agentNotices.hold(caller.session.ID, what, time.Now())
}

func (d *Daemon) noticeAfter(ctx context.Context, caller *mcpCaller, what string, err error) {
	if what != "" && err == nil {
		d.notifyAgentAction(ctx, caller.session.ID, what)
	}
}

func (d *Daemon) notifyAgentAction(ctx context.Context, callerID, what string) {
	message := fmt.Sprintf("%s: %s.", d.resolveDisplay(callerID), strings.TrimSuffix(what, "."))
	d.tellPerson(ctx, "Agent acted", message, callerID, "agent.acted")
}

// tellPerson notifies the person on the Mac and on every paired device.
func (d *Daemon) tellPerson(ctx context.Context, title, message, sessionID, eventType string) {
	key := strings.ReplaceAll(eventType, ".", "-") + "-" + uuid.NewString()
	if d.notifyDesktop != nil {
		if err := d.notifyDesktop(title, message, sessionID, "agenthail-"+key, "", nil); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
	}
	notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	d.notifyPairedDevices(notifyCtx, mobileNotification{Title: title, Message: message, SessionID: sessionID, EventType: eventType, CollapseID: key})
}
