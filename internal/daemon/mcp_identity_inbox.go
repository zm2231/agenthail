package daemon

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	mcpInboxBudget    = 12 << 10
	mcpInboxItemCap   = 4 << 10
	mcpInboxRefPrefix = "inbox:"
)

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
	Truncated  bool   `json:"truncated,omitempty"`
	BodyRef    string `json:"bodyRef,omitempty"`
	Bytes      int    `json:"bytes,omitempty"`
	ReceivedAt string `json:"receivedAt"`
}

func encodedLen(value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(encoded)
}

// clipEncoded keeps the longest prefix of text whose JSON string fits limit,
// since escapes can make the encoding several times the text.
func clipEncoded(text string, limit int) (string, bool) {
	if encodedLen(text) <= limit {
		return text, false
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if encodedLen(string(runes[:mid])) <= limit {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low]), true
}

// Unacknowledged messages come back, so a lost response loses none.
func (d *Daemon) mcpReadInbox(identity registry.AgentIdentity, since *uint64) (any, error) {
	if err := d.ackInbox(identity, since); err != nil {
		return nil, err
	}
	unread, err := d.Registry.ReadInbox(identity.Name, registry.InboxQuery{Peek: true})
	if err != nil {
		return nil, err
	}
	result := map[string]any{"identity": "@" + identity.Name}
	items := []mcpInboxItem{}
	for _, message := range unread {
		sender := message.SenderName
		if sender == "" {
			sender = message.SenderID
		}
		item := mcpInboxItem{ID: message.ID, From: sender, ReceivedAt: message.ReceivedAt.UTC().Format(time.RFC3339Nano)}
		item.Text, item.Truncated = clipEncoded(message.Message, mcpInboxItemCap)
		if item.Truncated {
			item.BodyRef, item.Bytes = mcpInboxRefPrefix+strconv.FormatInt(message.ID, 10), len(message.Message)
		}
		result["messages"], result["remaining"], result["latestSeq"] = append(items, item), len(unread)-len(items)-1, message.ID
		result["next"] = mcpInboxNext
		if encodedLen(result) > mcpInboxBudget && len(items) > 0 {
			break
		}
		items = append(items, item)
	}
	result["messages"], result["remaining"] = items, len(unread)-len(items)
	if len(items) == 0 {
		delete(result, "latestSeq")
		delete(result, "next")
	} else {
		result["latestSeq"] = items[len(items)-1].ID
	}
	return result, nil
}

const mcpInboxNext = "Pass latestSeq as since on your next agenthail_read, or as cursor to agenthail_wait, to mark these read; until then they are returned again. A truncated message pages through agenthail_read with its bodyRef. Reply with agenthail_send to each sender."

func (d *Daemon) ackInbox(identity registry.AgentIdentity, since *uint64) error {
	if since == nil {
		return nil
	}
	if *since > uint64(1<<62) {
		return refuse("invalid", "since must be a latestSeq an inbox read returned.")
	}
	_, err := d.Registry.AckInbox(identity.Name, int64(*since))
	return err
}

func (d *Daemon) mcpReadInboxBody(identity registry.AgentIdentity, ref string, offset int) (any, error) {
	id, err := strconv.ParseInt(strings.TrimPrefix(ref, mcpInboxRefPrefix), 10, 64)
	if err != nil {
		return nil, refuse("invalid", "That bodyRef isn't one an inbox read returned.")
	}
	if offset < 0 {
		return nil, refuse("invalid", "offset must not be negative.")
	}
	message, err := d.Registry.InboxMessageByID(identity.Name, id)
	if err != nil {
		return nil, refuse("not_found", err.Error())
	}
	text := message.Message
	if offset > len(text) {
		offset = len(text)
	}
	for offset > 0 && offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset--
	}
	chunk, more := clipEncoded(text[offset:], mcpInboxBudget)
	result := map[string]any{"id": id, "bodyRef": ref, "offset": offset, "bytes": len(text), "text": chunk}
	if more {
		result["nextOffset"] = offset + len(chunk)
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
	if cursor := strings.TrimSpace(input.Cursor); cursor != "" {
		since, err := strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return nil, refuse("invalid", "cursor must be a latestSeq an inbox read returned.")
		}
		if err := d.ackInbox(identity, &since); err != nil {
			return nil, err
		}
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
			return map[string]any{"type": "inbox", "unread": unread, "next": "Call agenthail_read with no sessionId to read them."}, nil
		}
		if !time.Now().Before(deadline) {
			return map[string]any{"type": "timeout", "unread": 0, "next": "Nothing arrived; call agenthail_wait again to keep waiting. Nothing is pushed to you."}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
