package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const mcpInboxBudget = 12 << 10

func (d *Daemon) callerIdentity(caller *mcpCaller) (registry.AgentIdentity, bool) {
	if caller == nil || !surface.IsAgentIdentity(&caller.session) {
		return registry.AgentIdentity{}, false
	}
	identity, err := d.Registry.AgentIdentityOf(caller.session.ID)
	return identity, err == nil
}

type mcpInboxItem struct {
	ID         int64  `json:"id"`
	From       string `json:"from,omitempty"`
	Text       string `json:"text"`
	ReceivedAt string `json:"receivedAt"`
}

func (d *Daemon) mcpReadInbox(identity registry.AgentIdentity) (any, error) {
	unread, err := d.Registry.ReadInbox(identity.Name, registry.InboxQuery{Peek: true})
	if err != nil {
		return nil, err
	}
	items := []mcpInboxItem{}
	ids := []int64{}
	used := 0
	for _, message := range unread {
		text := message.Message
		if len(text) > mcpItemTextLimit {
			text = clip(text, mcpItemTextLimit/4)
		}
		if used+len(text) > mcpInboxBudget && len(items) > 0 {
			break
		}
		used += len(text)
		sender := message.SenderName
		if sender == "" {
			sender = message.SenderID
		}
		items = append(items, mcpInboxItem{ID: message.ID, From: sender, Text: text, ReceivedAt: message.ReceivedAt.UTC().Format(time.RFC3339Nano)})
		ids = append(ids, message.ID)
	}
	if err := d.Registry.MarkInboxRead(identity.Name, ids); err != nil {
		return nil, err
	}
	result := map[string]any{"identity": "@" + identity.Name, "inbox": remoteInboxURI(identity.Name), "messages": items, "remaining": len(unread) - len(items)}
	if len(unread) > len(items) {
		result["next"] = "Call agenthail_read again for the rest."
	} else if len(items) > 0 {
		result["next"] = "Reply with agenthail_send to the sender each message names."
	}
	return result, nil
}

func (d *Daemon) mcpWaitInbox(ctx context.Context, caller *mcpCaller, identity registry.AgentIdentity, input mcpWaitInput) (any, error) {
	seconds := mcpWaitDefaultSec
	if input.TimeoutSec != nil {
		seconds = *input.TimeoutSec
	}
	if seconds < 0 {
		return nil, refuse("invalid", "timeoutSec can't be negative.")
	}
	deadline := time.Now().Add(time.Duration(min(seconds, mcpWaitLimitSec(caller.session))) * time.Second)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		unread, err := d.Registry.UnreadInbox(identity.Name)
		if err != nil {
			return nil, err
		}
		if unread > 0 {
			return map[string]any{"type": "inbox", "unread": unread, "inbox": remoteInboxURI(identity.Name), "next": "Call agenthail_read with no sessionId to read and mark them read."}, nil
		}
		if !time.Now().Before(deadline) {
			return map[string]any{"type": "timeout", "unread": 0, "next": "Nothing arrived; wait again, or end your turn and be told when a message arrives."}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
