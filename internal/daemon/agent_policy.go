package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/dashconfig"
)

func (d *Daemon) agentPolicy() *dashconfig.AgentPolicy {
	config, err := dashconfig.Load()
	if err != nil {
		d.log.Printf("read the agent policy: %s", err)
		return nil
	}
	return config.Agents
}

// agentNotices holds what to tell the person once an agent's action that
// the CLI runs has succeeded; the CLI names it back by token.
type agentNotices struct {
	mu      sync.Mutex
	pending map[string]agentNotice
}

type agentNotice struct {
	callerID string
	what     string
	expires  time.Time
}

// A caller holds at most agentNoticesPerCaller unredeemed notices; a
// command whose action fails never redeems its own, so the oldest goes.
const (
	agentNoticeLifetime   = 10 * time.Minute
	agentNoticesPerCaller = 16
)

func (n *agentNotices) hold(callerID, what string, now time.Time) string {
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

func (n *agentNotices) take(token, callerID string, now time.Time) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	notice, found := n.pending[token]
	if !found || notice.callerID != callerID || now.After(notice.expires) {
		return "", false
	}
	delete(n.pending, token)
	return notice.what, true
}

func (d *Daemon) holdAgentNotice(caller *mcpCaller, what string) string {
	if what == "" {
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
	key := "agent-acted-" + uuid.NewString()
	if d.notifyDesktop != nil {
		if err := d.notifyDesktop("Agent acted", message, callerID, "agenthail-"+key, "", nil); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
	}
	notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	d.notifyPairedDevices(notifyCtx, mobileNotification{Title: "Agent acted", Message: message, SessionID: callerID, EventType: "agent.acted", CollapseID: key})
}
