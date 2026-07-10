package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (n *Notion) Observe(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	messageID, reply, err := n.latestAgentReply(ctx, sess)
	if err != nil {
		return nil, err
	}
	return &surface.TurnObservation{
		Status:          surface.StatusIdle,
		CompletedTurnID: messageID,
		Reply:           reply,
	}, nil
}

func (n *Notion) threadExists(ctx context.Context, threadID string) (bool, error) {
	if err := n.ensureContext(ctx); err != nil {
		return false, err
	}
	body, _ := json.Marshal(map[string]any{"requests": []map[string]any{{"pointer": map[string]any{"table": "thread", "id": threadID}, "version": -1}}})
	status, responseBody, err := sidecarRequestWithCookies(ctx, "POST", n.inferenceURL("syncRecordValues"), n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return false, fmt.Errorf("resolve Notion thread: %w", err)
	}
	if status != 200 {
		return false, fmt.Errorf("resolve Notion thread (HTTP %d): %s", status, diagnosticExcerpt(responseBody))
	}
	var response struct {
		RecordMap struct {
			Thread map[string]json.RawMessage `json:"thread"`
		} `json:"recordMap"`
	}
	if err := json.Unmarshal([]byte(responseBody), &response); err != nil {
		return false, fmt.Errorf("parse Notion thread resolution: %w", err)
	}
	_, found := response.RecordMap.Thread[threadID]
	return found, nil
}

func (n *Notion) latestAgentReply(ctx context.Context, sess *surface.Session) (string, *surface.ReplyResult, error) {
	if sess.ID == "" || strings.HasPrefix(sess.ID, "new") {
		return "", nil, nil
	}
	if err := n.ensureContext(ctx); err != nil {
		return "", nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"requests": []map[string]any{{"pointer": map[string]any{"table": "thread", "id": sess.ID}, "version": -1}},
	})
	status, responseBody, err := sidecarRequestWithCookies(ctx,
		"POST", n.inferenceURL("syncRecordValues"), n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return "", nil, fmt.Errorf("notion reply: %w", err)
	}
	if status != 200 {
		return "", nil, fmt.Errorf("notion reply (HTTP %d): %s", status, diagnosticExcerpt(responseBody))
	}
	var threadResponse struct {
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
	if err := json.Unmarshal([]byte(responseBody), &threadResponse); err != nil {
		return "", nil, fmt.Errorf("parse Notion thread: %w", err)
	}
	record, ok := threadResponse.RecordMap.Thread[sess.ID]
	if !ok || len(record.Value.Value.Messages) == 0 {
		return "", nil, nil
	}
	messageIDs := record.Value.Value.Messages
	requests := make([]map[string]any, len(messageIDs))
	for i, id := range messageIDs {
		requests[i] = map[string]any{"pointer": map[string]any{"table": "thread_message", "id": id}, "version": -1}
	}
	body, _ = json.Marshal(map[string]any{"requests": requests})
	status, responseBody, err = sidecarRequestWithCookies(ctx,
		"POST", n.inferenceURL("syncRecordValues"), n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return "", nil, fmt.Errorf("notion reply messages: %w", err)
	}
	if status != 200 {
		return "", nil, fmt.Errorf("notion reply messages (HTTP %d): %s", status, diagnosticExcerpt(responseBody))
	}
	var messageResponse struct {
		RecordMap struct {
			ThreadMessage map[string]struct {
				Value struct {
					Value struct {
						Step struct {
							Type  string          `json:"type"`
							Value json.RawMessage `json:"value"`
						} `json:"step"`
					} `json:"value"`
				} `json:"value"`
			} `json:"thread_message"`
		} `json:"recordMap"`
	}
	if err := json.Unmarshal([]byte(responseBody), &messageResponse); err != nil {
		return "", nil, fmt.Errorf("parse Notion messages: %w", err)
	}
	for i := len(messageIDs) - 1; i >= 0; i-- {
		id := messageIDs[i]
		message, ok := messageResponse.RecordMap.ThreadMessage[id]
		if !ok || message.Value.Value.Step.Type != "agent-inference" {
			continue
		}
		var items []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(message.Value.Value.Step.Value, &items); err != nil {
			continue
		}
		for _, item := range items {
			if item.Type == "text" && strings.TrimSpace(item.Content) != "" {
				text := strings.TrimSpace(langTagRe.ReplaceAllString(item.Content, ""))
				return id, &surface.ReplyResult{Text: text, Done: true}, nil
			}
		}
	}
	return "", nil, nil
}
