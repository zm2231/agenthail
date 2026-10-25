package surfaces

import (
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// claudeAPIErrorNotice turns the message Claude Code writes in place of a
// reply the API refused into a notice, so a usage limit reads as a
// status rather than as something the agent said.
func claudeAPIErrorNotice(record map[string]any) surface.TimelineItem {
	message, _ := record["message"].(map[string]any)
	item := surface.TimelineItem{Kind: "notice", Title: "Claude API error", Text: claudeMessageText(message["content"]), Status: "api_error"}
	if claudeUsageLimitRecord(record) {
		item.Title, item.Status = "Usage limit reached", surface.TimelineUsageLimit
		if resetsAt := claudeUsageLimitResetsAt(record); !resetsAt.IsZero() {
			item.ResetsAt = resetsAt.UTC().Format(time.RFC3339)
		}
	}
	return item
}

func claudeUsageLimitRecord(record map[string]any) bool {
	return str(record, "error") == "rate_limit" || str(record, "apiError") == "usage_limit_reached"
}

func claudeUsageLimitResetsAt(record map[string]any) time.Time {
	for _, limits := range []any{record["quotaLimits"], claudeNestedField(record, "apiErrorParams", "rate_limit_info")} {
		if limits, ok := limits.(map[string]any); ok {
			if seconds, ok := limits["resetsAt"].(float64); ok && seconds > 0 {
				return time.Unix(int64(seconds), 0)
			}
		}
	}
	return time.Time{}
}

func claudeNestedField(record map[string]any, keys ...string) any {
	var value any = record
	for _, key := range keys {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

// claudeImageSourceCompanion reports Claude's meta records that only describe
// an image shown elsewhere: where a pasted image was saved, or how a read
// image was resized. The image renders as an attachment, and a saved path is
// local to that machine.
func claudeImageSourceCompanion(record, message map[string]any) bool {
	if meta, _ := record["isMeta"].(bool); !meta {
		return false
	}
	if text, ok := message["content"].(string); ok {
		return claudeImageNote(text)
	}
	blocks, _ := message["content"].([]any)
	if len(blocks) == 0 {
		return false
	}
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if str(block, "type") != "text" || !claudeImageNote(str(block, "text")) {
			return false
		}
	}
	return true
}

// claudeImageNote reports Claude Code's notes about an image it saved or
// resized ("[Image: source: ...]", "[Image: original WxH, displayed at ...]").
func claudeImageNote(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "[Image: ") && strings.HasSuffix(text, "]") && !strings.Contains(text, "\n")
}
