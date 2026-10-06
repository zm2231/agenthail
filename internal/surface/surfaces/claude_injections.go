package surfaces

import (
	"regexp"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	claudeOriginHuman            = "human"
	claudeOriginTaskNotification = "task-notification"
	claudeOriginPeer             = "peer"
	claudeOriginAutoContinuation = "auto-continuation"
)

var (
	claudeNotificationTag = regexp.MustCompile(`(?s)<(task-id|tool-use-id|status|summary)>(.*?)</(?:task-id|tool-use-id|status|summary)>`)
	claudePeerEnvelope    = regexp.MustCompile(`(?s)<cross-session-message\b([^>]*)>(.*?)</cross-session-message>`)
	claudePeerName        = regexp.MustCompile(`from-name="([^"]*)"`)
)

// claudePromptOrigin names who injected a user-role prompt. Claude Code
// records it in origin.kind; a record without one is a human prompt unless
// it is a task notification envelope.
func claudePromptOrigin(origin map[string]any, text string) string {
	if kind := str(origin, "kind"); kind != "" {
		return kind
	}
	if strings.HasPrefix(strings.TrimSpace(text), "<task-notification>") {
		return claudeOriginTaskNotification
	}
	return claudeOriginHuman
}

// claudeInjectedItem renders a prompt Claude Code injected on the user's
// behalf. Notifications and peer messages carry a provider identity so the
// queued copy and the delivered copy of one injection share an item ID.
func claudeInjectedItem(kind string, origin map[string]any, text string) surface.TimelineItem {
	switch kind {
	case claudeOriginTaskNotification:
		tags := map[string]string{}
		for _, match := range claudeNotificationTag.FindAllStringSubmatch(text, -1) {
			if _, seen := tags[match[1]]; !seen {
				tags[match[1]] = strings.TrimSpace(match[2])
			}
		}
		item := surface.TimelineItem{Kind: "event", Role: "system", Origin: kind, Title: "Task notification", Text: tags["summary"], Status: tags["status"], CallID: tags["tool-use-id"]}
		if item.Text == "" {
			item.Text = strings.TrimSpace(text)
		}
		if taskID := tags["task-id"]; taskID != "" {
			item.ID = "task-notification:" + taskID + ":" + item.Status
		}
		return item
	case claudeOriginPeer:
		sender := str(origin, "name")
		body := str(origin, "body")
		if envelope := claudePeerEnvelope.FindStringSubmatch(text); envelope != nil {
			if body == "" {
				body = strings.TrimSpace(envelope[2])
			}
			if name := claudePeerName.FindStringSubmatch(envelope[1]); sender == "" && name != nil {
				sender = name[1]
			}
		}
		if body == "" {
			body = strings.TrimSpace(text)
		}
		item := surface.TimelineItem{Kind: "message", Role: "peer", Origin: kind, Sender: sender, Title: sender, Text: body}
		if item.Title == "" {
			item.Title = "Agent message"
		}
		if id := str(origin, "msg_id"); id != "" {
			item.ID = "peer:" + id
		}
		return item
	case claudeOriginAutoContinuation:
		return surface.TimelineItem{Kind: "event", Role: "system", Origin: kind, Title: "Continued automatically", Text: strings.TrimSpace(text)}
	default:
		return surface.TimelineItem{Kind: "event", Role: "system", Origin: kind, Title: "Injected prompt", Text: strings.TrimSpace(text)}
	}
}

// claudeQueuedCommandItems renders a prompt queued into a running turn. Human
// prompts keep their existing presentation and are not rendered here.
func claudeQueuedCommandItems(record map[string]any) []surface.TimelineItem {
	attachment, _ := record["attachment"].(map[string]any)
	if str(attachment, "type") != "queued_command" {
		return nil
	}
	prompt, ok := attachment["prompt"].(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return nil
	}
	origin, _ := attachment["origin"].(map[string]any)
	kind := claudePromptOrigin(origin, prompt)
	if kind == claudeOriginHuman {
		return nil
	}
	return []surface.TimelineItem{claudeInjectedItem(kind, origin, prompt)}
}

func claudeMessageText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	blocks, _ := content.([]any)
	var parts []string
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if str(block, "type") == "text" {
			parts = append(parts, str(block, "text"))
		}
	}
	return strings.Join(parts, "\n\n")
}
