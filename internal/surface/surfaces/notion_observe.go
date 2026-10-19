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
		Status:          surface.StatusUnknown,
		CompletedTurnID: messageID,
		Reply:           reply,
	}, nil
}

func (n *Notion) threadExists(ctx context.Context, threadID string) (bool, error) {
	account, err := n.currentAccount(ctx)
	if err != nil {
		return false, err
	}
	body, _ := json.Marshal(map[string]any{"requests": []map[string]any{{"pointer": map[string]any{"table": "thread", "id": threadID}, "version": -1}}})
	status, responseBody, err := sidecarRequestWithCookies(ctx, "POST", notionAPI("syncRecordValues"), account.headers(), string(body), account.cookies, 15*time.Second)
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

func isNewNotionThread(sess *surface.Session) bool {
	return sess.ID == "" || strings.HasPrefix(sess.ID, "new")
}

func (n *Notion) latestAgentReply(ctx context.Context, sess *surface.Session) (string, *surface.ReplyResult, error) {
	if isNewNotionThread(sess) {
		return "", nil, nil
	}
	account, err := n.currentAccount(ctx)
	if err != nil {
		return "", nil, err
	}
	return account.latestAgentReply(ctx, sess)
}

func (a notionAccount) latestAgentReply(ctx context.Context, sess *surface.Session) (string, *surface.ReplyResult, error) {
	events, err := a.agentCompletions(ctx, sess, "", false)
	if err != nil || len(events) == 0 {
		return "", nil, err
	}
	last := events[len(events)-1]
	return last.ID, &surface.ReplyResult{Text: last.Text, Done: true}, nil
}

func (n *Notion) ReadCompletions(ctx context.Context, sess *surface.Session, cursor string) (surface.CompletionBatch, error) {
	var after string
	if cursor != "" {
		if err := json.Unmarshal([]byte(cursor), &after); err != nil {
			return surface.CompletionBatch{}, err
		}
	}
	var events []surface.Completion
	if !isNewNotionThread(sess) {
		account, err := n.currentAccount(ctx)
		if err != nil {
			return surface.CompletionBatch{}, err
		}
		if events, err = account.agentCompletions(ctx, sess, after, cursor != ""); err != nil {
			return surface.CompletionBatch{}, err
		}
	}
	last := after
	if len(events) > 0 {
		last = events[len(events)-1].ID
	}
	encoded, _ := json.Marshal(last)
	batch := surface.CompletionBatch{Cursor: string(encoded)}
	if cursor == "" {
		return batch, nil
	}
	batch.Events = events
	return batch, nil
}

func (a notionAccount) agentCompletions(ctx context.Context, sess *surface.Session, after string, requireComplete bool) ([]surface.Completion, error) {
	if isNewNotionThread(sess) {
		return nil, nil
	}
	body, _ := json.Marshal(map[string]any{
		"requests": []map[string]any{{"pointer": map[string]any{"table": "thread", "id": sess.ID}, "version": -1}},
	})
	status, responseBody, err := notionRecordRequest(ctx,
		"POST", notionAPI("syncRecordValues"), a.headers(), string(body), a.cookies, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("notion reply: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("notion reply (HTTP %d): %s", status, diagnosticExcerpt(responseBody))
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
		return nil, fmt.Errorf("parse Notion thread: %w", err)
	}
	record, ok := threadResponse.RecordMap.Thread[sess.ID]
	if !ok {
		return nil, fmt.Errorf("Notion thread is missing from provider response")
	}
	messageIDs := record.Value.Value.Messages
	if after != "" {
		boundary := -1
		for i, id := range messageIDs {
			if id == after {
				boundary = i
				break
			}
		}
		if boundary < 0 {
			return nil, fmt.Errorf("saved relay message is missing from Notion history")
		}
		messageIDs = messageIDs[boundary+1:]
	}
	if len(messageIDs) == 0 {
		return nil, nil
	}
	requests := make([]map[string]any, len(messageIDs))
	for i, id := range messageIDs {
		requests[i] = map[string]any{"pointer": map[string]any{"table": "thread_message", "id": id}, "version": -1}
	}
	body, _ = json.Marshal(map[string]any{"requests": requests})
	status, responseBody, err = notionRecordRequest(ctx,
		"POST", notionAPI("syncRecordValues"), a.headers(), string(body), a.cookies, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("notion reply messages: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("notion reply messages (HTTP %d): %s", status, diagnosticExcerpt(responseBody))
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
		return nil, fmt.Errorf("parse Notion messages: %w", err)
	}
	var completions []surface.Completion
	inputID, user := "", ""
	for i := 0; i < len(messageIDs); i++ {
		id := messageIDs[i]
		message, ok := messageResponse.RecordMap.ThreadMessage[id]
		if !ok {
			if requireComplete {
				return nil, fmt.Errorf("Notion relay message %s is missing from provider response", id)
			}
			continue
		}
		if message.Value.Value.Step.Type == "user" {
			var parts [][]string
			if err := json.Unmarshal(message.Value.Value.Step.Value, &parts); err != nil {
				if requireComplete {
					return nil, err
				}
				continue
			}
			inputID, user = id, ""
			if len(parts) > 0 && len(parts[0]) > 0 {
				user = parts[0][0]
			}
			continue
		}
		if message.Value.Value.Step.Type != "agent-inference" {
			continue
		}
		var items []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(message.Value.Value.Step.Value, &items); err != nil {
			if requireComplete {
				return nil, err
			}
			continue
		}
		var parts []string
		for _, item := range items {
			if item.Type == "text" {
				parts = append(parts, strings.TrimSpace(langTagRe.ReplaceAllString(item.Content, "")))
			}
		}
		text := strings.TrimSpace(strings.Join(parts, "\n"))
		if text != "" {
			completions = append(completions, surface.Completion{ID: id, InputID: inputID, User: user, Text: text})
		}
	}
	return completions, nil
}
