package surfaces

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	var commands *commandsWatch
	if uuid == "" {
		onEvent = serializedEvents(onEvent)
		listed := *sess
		commands = watchCommands(ctx, func(ctx context.Context) (*surface.RuntimeCommands, error) { return c.Commands(ctx, &listed) }, onEvent)
		defer commands.Stop()
	}
	if sess.Transport == codexTransportManaged && uuid != "" {
		return c.followManagedTurn(ctx, sess, uuid, onEvent, timeout)
	}
	if sess.Transport == codexTransportManaged {
		stateContext, stopState := context.WithCancel(ctx)
		watched := make(chan struct{})
		stateSession := *sess
		go func() {
			defer close(watched)
			c.watchManagedState(stateContext, stateSession, onEvent, commands)
		}()
		defer func() {
			stopState()
			<-watched
		}()
	}
	if !sess.TranscriptOffsetSet && sess.TranscriptFromStart {
		if err := c.home.awaitRollout(ctx, sess, timeout); err != nil {
			return err
		}
		sess.TranscriptOffset = 0
		sess.TranscriptOffsetSet = true
	}
	if !sess.TranscriptOffsetSet {
		read, err := c.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: timelineItemLimit})
		if err != nil {
			return err
		}
		if read == nil || !read.TranscriptOffsetSet {
			return fmt.Errorf("Codex local transcript is unavailable: %w", surface.ErrTranscriptUnavailable)
		}
		sess.TranscriptOffset = read.TranscriptOffset
		sess.TranscriptOffsetSet = true
		sess.CodexPendingEventUser = read.CodexPendingEventUser
		sess.CodexPendingEventTurn = read.CodexPendingEventTurn
		sess.CodexCurrentTurnID = read.CodexCurrentTurnID
	}
	return c.streamTranscript(ctx, sess, uuid, onEvent, timeout)
}

// awaitRollout waits for a thread's rollout, which Codex writes only once the
// thread's first turn starts.
func (h CodexHome) awaitRollout(ctx context.Context, sess *surface.Session, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if path := h.transcriptPath(sess); path != "" && fileExists(path) {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("Codex rollout was not created within %s: %w", timeout, surface.ErrStreamWindow)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(transcriptPollInterval):
		}
	}
}

func codexDesktopStreamKey(turnID, kind, itemID string) string {
	kind = codexDesktopKeyKind(kind)
	if turnID == "" {
		return "renderer:" + kind + ":" + itemID
	}
	key := "codex:" + turnID + ":" + kind
	if itemID != "" {
		key += ":" + itemID
	}
	return key
}

func codexDesktopKeyKind(kind string) string {
	switch kind {
	case "text", "assistant":
		return "assistant"
	case "tool_use", "toolCall":
		return "toolCall"
	case "tool_result", "toolResult":
		return "toolResult"
	default:
		return kind
	}
}

func codexManagedAuthoritativeStreamEvent(turnID string, item codexAssistantItem) surface.StreamEvent {
	return codexAuthoritativeStreamEventWithKey("managed:"+turnID+":text", turnID, item)
}

func codexAuthoritativeStreamEventWithKey(key, turnID string, item codexAssistantItem) surface.StreamEvent {
	if item.ID != "" {
		if strings.HasPrefix(key, "codex:") {
			key += ":" + item.ID
		}
	}
	return surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(len(item.Text)), Operation: "upsert", Final: true, TurnID: turnID, Kind: "text", Text: item.Text}
}

func codexTurnByID(thread *codexThread, turnID string) *codexTurn {
	if thread == nil || turnID == "" {
		return nil
	}
	for index := range thread.Turns {
		if thread.Turns[index].ID == turnID {
			return &thread.Turns[index]
		}
	}
	return nil
}
