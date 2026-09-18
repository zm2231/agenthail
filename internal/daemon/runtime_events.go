package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

const maxTimelinePages = 10000

type canonicalRuntimeEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	ID        string `json:"id,omitempty"`
	Role      string `json:"role,omitempty"`
	Text      string `json:"text,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	Output    any    `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
	Source    string `json:"source,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (d *Daemon) publishTimelineEvents(ctx context.Context, adapter surface.Surface, session *surface.Session) error {
	provider, ok := adapter.(surface.TimelineProvider)
	if !ok || !surface.EffectiveCapabilities(session, adapter.Capabilities()).Stream {
		return nil
	}
	var pages []*surface.SessionTimeline
	var before int64
	for pageNumber := 0; pageNumber < maxTimelinePages; pageNumber++ {
		timeline, err := provider.Timeline(ctx, session, before)
		if err != nil {
			return err
		}
		if timeline == nil {
			return fmt.Errorf("timeline provider returned no result")
		}
		if timeline.UnavailableReason != "" {
			return nil
		}
		pages = append(pages, timeline)
		pageHasProcessedItem := false
		for _, item := range timeline.Items {
			if item.ID == "" {
				return fmt.Errorf("timeline item has no stable id")
			}
			processed, err := d.Registry.HasDaemonEventItem("runtime:" + session.ID + ":" + item.ID)
			if err != nil {
				return err
			}
			pageHasProcessedItem = pageHasProcessedItem || processed
		}
		if pageHasProcessedItem {
			break
		}
		if timeline.NextBefore == 0 {
			if timeline.Truncated {
				return fmt.Errorf("timeline provider returned truncated activity without a pagination cursor")
			}
			break
		}
		if timeline.NextBefore >= before && before != 0 {
			return fmt.Errorf("timeline provider did not advance pagination cursor")
		}
		before = timeline.NextBefore
		if pageNumber == maxTimelinePages-1 {
			return fmt.Errorf("timeline provider exceeded pagination limit")
		}
	}
	for page := len(pages) - 1; page >= 0; page-- {
		for _, item := range pages[page].Items {
			event, ok := canonicalRuntimeEventForItem(session.ID, item)
			if !ok {
				continue
			}
			if _, err := d.events.publishWithKey(event.Type, session.ID, event, "runtime:"+session.ID+":"+item.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func canonicalRuntimeEventForItem(sessionID string, item surface.TimelineItem) (canonicalRuntimeEvent, bool) {
	if sessionID == "" || item.ID == "" {
		return canonicalRuntimeEvent{}, false
	}
	event := canonicalRuntimeEvent{SessionID: sessionID, ID: item.ID, Role: item.Role, Text: item.Text, Name: item.Title, Truncated: item.Truncated}
	switch item.Kind {
	case "message":
		event.Type = "message"
	case "reasoning":
		event.Type = "thought"
	case "toolCall":
		event.Type = "tool_start"
		if item.CallID != "" {
			event.ID = item.CallID
		}
		event.Input = decodeTimelineValue(item.Text)
	case "toolResult":
		event.Type = "tool_done"
		if item.CallID != "" {
			event.ID = item.CallID
		}
		event.Output = decodeTimelineValue(item.Text)
		if item.Status == "error" {
			event.Error = item.Text
			event.Output = nil
		}
	default:
		return canonicalRuntimeEvent{}, false
	}
	return event, true
}

func decodeTimelineValue(value string) any {
	var decoded any
	if value != "" && json.Unmarshal([]byte(value), &decoded) == nil {
		return decoded
	}
	return value
}
