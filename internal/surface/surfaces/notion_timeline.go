package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const notionTimelineMessageBudget = 200

var notionRecordRequest = sidecarRequestWithCookies

type notionThreadMessage struct {
	ID        string
	Type      string
	Text      string
	Timestamp time.Time
}

func (n *Notion) ReadSession(ctx context.Context, sess *surface.Session, request surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	result := &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}, Source: "notion-remote"}
	if request.Limit < 1 {
		request.Limit = 1
	}
	if sess == nil || sess.ID == "" {
		return nil, fmt.Errorf("notion session read: no thread ID")
	}
	if err := n.ensureContext(ctx); err != nil {
		return nil, err
	}

	messageIDs, err := n.notionThreadMessageIDs(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	end := len(messageIDs)
	if request.Before > 0 {
		if request.Before > int64(len(messageIDs)) {
			return nil, fmt.Errorf("notion thread changed; refresh the session")
		}
		end = int(request.Before)
	}
	budget := request.Limit
	if budget > notionTimelineMessageBudget/2 {
		budget = notionTimelineMessageBudget / 2
	}
	budget *= 2
	start := end - budget
	if start < 0 {
		start = 0
	}
	if start > 0 {
		result.NextBefore = int64(start)
	}
	if start == end {
		return surface.BoundSessionRead(sess, result), nil
	}

	messages, err := n.notionThreadMessages(ctx, messageIDs[start:end])
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(message.Text) == "" {
			continue
		}
		role := ""
		switch message.Type {
		case "user":
			role = "user"
		case "agent-inference":
			role = "assistant"
		default:
			continue
		}
		timestamp := ""
		if !message.Timestamp.IsZero() {
			timestamp = message.Timestamp.Format(time.RFC3339Nano)
		}
		result.Items = append(result.Items, surface.TimelineItem{ID: message.ID, Kind: "message", Role: role, Title: role, Text: message.Text, Timestamp: timestamp})
		if role == "user" {
			if len(result.Exchanges) == 0 || result.Exchanges[len(result.Exchanges)-1].Assistant != "" {
				result.Exchanges = append(result.Exchanges, surface.Exchange{User: message.Text, Timestamp: message.Timestamp, Source: result.Source})
			} else {
				result.Exchanges[len(result.Exchanges)-1].User = message.Text
			}
		} else if len(result.Exchanges) == 0 {
			result.Exchanges = append(result.Exchanges, surface.Exchange{Assistant: message.Text, Timestamp: message.Timestamp, Source: result.Source})
		} else if result.Exchanges[len(result.Exchanges)-1].Assistant == "" {
			result.Exchanges[len(result.Exchanges)-1].Assistant = message.Text
		} else {
			result.Exchanges = append(result.Exchanges, surface.Exchange{Assistant: message.Text, Timestamp: message.Timestamp, Source: result.Source})
		}
	}
	return surface.BoundSessionRead(sess, result), nil
}

func (n *Notion) notionThreadMessageIDs(ctx context.Context, threadID string) ([]string, error) {
	body, _ := json.Marshal(map[string]any{"requests": []map[string]any{{"pointer": map[string]any{"table": "thread", "id": threadID}, "version": -1}}})
	status, responseBody, err := notionRecordRequest(ctx, "POST", n.inferenceURL("syncRecordValues"), n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("notion session read thread: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("notion session read thread (HTTP %d)", status)
	}
	var response struct {
		RecordMap struct {
			Thread map[string]struct {
				Value struct {
					Value struct {
						Messages []string `json:"messages"`
					} `json:"value"`
				} `json:"value"`
			} `json:"thread"`
		} `json:"recordMap"`
	}
	if err := json.Unmarshal([]byte(responseBody), &response); err != nil {
		return nil, fmt.Errorf("notion session read thread: parse response: %w", err)
	}
	thread, ok := response.RecordMap.Thread[threadID]
	if !ok {
		return nil, fmt.Errorf("notion session read: thread %s not found", threadID)
	}
	return thread.Value.Value.Messages, nil
}

func (n *Notion) notionThreadMessages(ctx context.Context, messageIDs []string) ([]notionThreadMessage, error) {
	requests := make([]map[string]any, len(messageIDs))
	for i, id := range messageIDs {
		requests[i] = map[string]any{"pointer": map[string]any{"table": "thread_message", "id": id}, "version": -1}
	}
	body, _ := json.Marshal(map[string]any{"requests": requests})
	status, responseBody, err := notionRecordRequest(ctx, "POST", n.inferenceURL("syncRecordValues"), n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("notion session read messages: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("notion session read messages (HTTP %d)", status)
	}
	var response struct {
		RecordMap struct {
			ThreadMessage map[string]struct {
				Value struct {
					Value struct {
						Step struct {
							Type      string          `json:"type"`
							Value     json.RawMessage `json:"value"`
							CreatedAt string          `json:"createdAt"`
						} `json:"step"`
					} `json:"value"`
				} `json:"value"`
			} `json:"thread_message"`
		} `json:"recordMap"`
	}
	if err := json.Unmarshal([]byte(responseBody), &response); err != nil {
		return nil, fmt.Errorf("notion session read messages: parse response: %w", err)
	}
	messages := make([]notionThreadMessage, 0, len(messageIDs))
	for _, id := range messageIDs {
		record, ok := response.RecordMap.ThreadMessage[id]
		if !ok {
			continue
		}
		step := record.Value.Value.Step
		text := ""
		switch step.Type {
		case "user":
			var items [][]string
			if json.Unmarshal(step.Value, &items) == nil && len(items) > 0 && len(items[0]) > 0 {
				text = strings.TrimSpace(items[0][0])
			}
		case "agent-inference":
			var items []struct {
				Type    string `json:"type"`
				Content string `json:"content"`
			}
			if json.Unmarshal(step.Value, &items) == nil {
				var parts []string
				for _, item := range items {
					if item.Type == "text" && strings.TrimSpace(item.Content) != "" {
						parts = append(parts, strings.TrimSpace(langTagRe.ReplaceAllString(item.Content, "")))
					}
				}
				text = strings.Join(parts, "\n")
			}
		}
		at, _ := time.Parse(time.RFC3339Nano, step.CreatedAt)
		messages = append(messages, notionThreadMessage{ID: id, Type: step.Type, Text: text, Timestamp: at})
	}
	return messages, nil
}
