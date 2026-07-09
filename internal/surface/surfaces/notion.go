package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

// Notion AI via internal API (runInferenceTranscript), proxied through curl_cffi.

var langTagRe = regexp.MustCompile(`(?i)^<lang[^>]*/?>\s*`)

type Notion struct {
	spaceID      string
	userID       string
	userName     string
	timezone     string
	cookieBridge string
}

func NewNotion(spaceID, userID string) *Notion {
	return &Notion{
		spaceID:  spaceID,
		userID:   userID,
		timezone: os.Getenv("AGENTHAIL_NOTION_TZ"),
	}
}

func (n *Notion) bridge() string {
	if n.cookieBridge == "" {
		n.cookieBridge = cookieBridgePath("cookie")
	}
	return n.cookieBridge
}

func (n *Notion) ensureContext() {
	if n.spaceID != "" && n.userID != "" {
		return
	}
	n.autoDetect()
}

func (n *Notion) autoDetect() {
	status, respBody, err := sidecarPostWithCookies(
		"https://app.notion.com/api/v3/getSpaces",
		n.headers(), "{}", n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil || status != 200 {
		return
	}
	var resp map[string]any
	if json.Unmarshal([]byte(respBody), &resp) != nil {
		return
	}
	for uid, udata := range resp {
		if uid == "isNotionError" || uid == "errorId" || uid == "name" || uid == "debugMessage" || uid == "message" {
			continue
		}
		if udataMap, ok := udata.(map[string]any); ok {
			if n.userID == "" {
				n.userID = uid
			}
			if n.userName == "" {
				if nu, ok := udataMap["notion_user"].(map[string]any); ok {
					if rec, ok := nu[uid].(map[string]any); ok {
						if val, ok := rec["value"].(map[string]any); ok {
							if inner, ok := val["value"].(map[string]any); ok {
								n.userName, _ = inner["name"].(string)
							}
						}
					}
				}
			}
			if n.timezone == "" {
				if us, ok := udataMap["user_settings"].(map[string]any); ok {
					if rec, ok := us[uid].(map[string]any); ok {
						if val, ok := rec["value"].(map[string]any); ok {
							if inner, ok := val["value"].(map[string]any); ok {
								if settings, ok := inner["settings"].(map[string]any); ok {
									n.timezone, _ = settings["time_zone"].(string)
								}
							}
						}
					}
				}
			}
			if spaces, ok := udataMap["space"].(map[string]any); ok {
				for sid := range spaces {
					if n.spaceID == "" {
						n.spaceID = sid
						break
					}
				}
			}
		}
		break
	}
}

func (n *Notion) Name() surface.SurfaceKind { return surface.KindNotion }

func (n *Notion) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: false, Reply: true,
		Interrupt: false, Steer: true, Model: true,
	}
}

func (n *Notion) headers() map[string]string {
	return map[string]string{
		"content-type":                "application/json",
		"x-notion-active-user-header": n.userID,
		"x-notion-space-id":           n.spaceID,
	}
}

func (n *Notion) inferenceURL(action string) string {
	return "https://app.notion.com/api/v3/" + action
}

func (n *Notion) List(ctx context.Context) ([]surface.Session, error) {
	n.ensureContext()
	body, _ := json.Marshal(map[string]any{
		"threadParentPointer": map[string]any{
			"table":   "space",
			"id":      n.spaceID,
			"spaceId": n.spaceID,
		},
		"limit":              50,
		"includeWriterChats": false,
	})
	status, respBody, err := sidecarPostWithCookies(
		n.inferenceURL("getInferenceTranscriptsForUser"),
		n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("list threads (HTTP %d): %s", status, respBody)
	}

	var resp struct {
		Transcripts []struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			UpdatedAt int64  `json:"updated_at"`
		} `json:"transcripts"`
	}
	if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
		return nil, fmt.Errorf("parse threads: %w", err)
	}

	sessions := make([]surface.Session, 0, len(resp.Transcripts))
	for _, t := range resp.Transcripts {
		sess := surface.Session{
			ID:      t.ID,
			Surface: surface.KindNotion,
			Name:    t.Title,
			Status:  surface.StatusIdle,
		}
		if t.UpdatedAt > 0 {
			sess.LastActive = time.UnixMilli(t.UpdatedAt)
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

func (n *Notion) Resolve(ctx context.Context, target string) (*surface.Session, error) {
	threads, err := n.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range threads {
		if t.ID == target || strings.HasPrefix(t.ID, target) {
			return &t, nil
		}
		if t.Name != "" && strings.EqualFold(t.Name, target) {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("no notion thread matched '%s'", target)
}

func (n *Notion) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	n.ensureContext()
	now := time.Now().UTC().Format(time.RFC3339)
	newThread := sess.ID == "" || strings.HasPrefix(sess.ID, "new")
	threadID := ""
	if !newThread {
		threadID = sess.ID
	}

	transcript := []map[string]any{
		{
			"id":   uuid.NewString(),
			"type": "config",
			"value": map[string]any{
				"type":                    "workflow",
				"model":                   "almond-croissant-low",
				"isCustomAgent":           false,
				"useWebSearch":            false,
				"enableCustomAgents":      false,
				"enableAgentTodos":        false,
				"enableAgentAutomations":  false,
				"enableAgentIntegrations": false,
			},
		},
		{
			"id":   uuid.NewString(),
			"type": "context",
			"value": map[string]any{
				"timezone":        n.timezone,
				"userName":        n.userName,
				"userId":          n.userID,
				"spaceId":         n.spaceID,
				"currentDatetime": now,
				"surface":         "ai_chat",
			},
		},
		{
			"id":        uuid.NewString(),
			"type":      "user",
			"value":     [][]string{{message}},
			"userId":    n.userID,
			"createdAt": now,
		},
	}

	reqBody := map[string]any{
		"traceId":                 uuid.NewString(),
		"spaceId":                 n.spaceID,
		"transcript":              transcript,
		"threadId":                threadID,
		"createThread":            newThread,
		"generateTitle":           false,
		"saveAllThreadOperations": true,
		"threadType":              "workflow",
		"isPartialTranscript":     true,
		"asPatchResponse":         true,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	headers := n.headers()
	headers["accept"] = "application/x-ndjson"

	status, respBody, err := sidecarPostWithCookies(
		n.inferenceURL("runInferenceTranscript"),
		headers, string(bodyBytes), n.bridge(), "https://app.notion.com/", 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("notion send: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("notion send (HTTP %d): %s", status, respBody)
	}

	resultThreadID := threadID
	content := parseNotionResponse(respBody)
	if resultThreadID == "" {
		resultThreadID = extractThreadID(respBody)
	}

	_ = content

	return &surface.SendResult{UUID: resultThreadID, Accepted: true}, nil
}

func (n *Notion) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	n.ensureContext()
	if sess.ID == "" {
		return &surface.ReplyResult{Error: "no thread ID"}, nil
	}

	body, _ := json.Marshal(map[string]any{
		"requests": []map[string]any{
			{"pointer": map[string]any{"table": "thread", "id": sess.ID}, "version": -1},
		},
	})
	status, respBody, err := sidecarPostWithCookies(
		n.inferenceURL("syncRecordValues"),
		n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("notion reply: %w", err)
	}
	if status != 200 {
		return &surface.ReplyResult{Error: fmt.Sprintf("HTTP %d", status)}, nil
	}

	var threadResp struct {
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
	if err := json.Unmarshal([]byte(respBody), &threadResp); err != nil {
		return &surface.ReplyResult{Error: "parse thread"}, nil
	}

	threadRec, ok := threadResp.RecordMap.Thread[sess.ID]
	if !ok || len(threadRec.Value.Value.Messages) == 0 {

		return &surface.ReplyResult{Error: "no messages"}, nil
	}

	msgIDs := threadRec.Value.Value.Messages
	if limit > 0 && limit < len(msgIDs) {
		msgIDs = msgIDs[max(0, len(msgIDs)-limit):]
	}
	if len(msgIDs) > 20 {
		msgIDs = msgIDs[len(msgIDs)-20:]
	}

	reqs := make([]map[string]any, len(msgIDs))
	for i, id := range msgIDs {
		reqs[i] = map[string]any{
			"pointer": map[string]any{"table": "thread_message", "id": id},
			"version": -1,
		}
	}
	body2, _ := json.Marshal(map[string]any{"requests": reqs})
	status2, respBody2, err := sidecarPostWithCookies(
		n.inferenceURL("syncRecordValues"),
		n.headers(), string(body2), n.bridge(), "https://app.notion.com/", 15*time.Second)

	if err != nil {
		return nil, fmt.Errorf("notion reply messages: %w", err)
	}
	if status2 != 200 {
		return &surface.ReplyResult{Error: fmt.Sprintf("HTTP %d", status2)}, nil
	}

	var msgResp struct {
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
	if err := json.Unmarshal([]byte(respBody2), &msgResp); err != nil {
		return &surface.ReplyResult{Error: "parse messages"}, nil
	}

	for i := len(msgIDs) - 1; i >= 0; i-- {
		rec, ok := msgResp.RecordMap.ThreadMessage[msgIDs[i]]
		if !ok {
			continue
		}
		step := rec.Value.Value.Step
		if step.Type != "agent-inference" {
			continue
		}
		var items []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(step.Value, &items); err != nil {
			continue
		}
		for _, item := range items {
			if item.Type == "text" && item.Content != "" {
				text := langTagRe.ReplaceAllString(item.Content, "")
				return &surface.ReplyResult{Text: strings.TrimSpace(text), Done: true}, nil
			}
		}
	}

	return &surface.ReplyResult{Error: "no assistant reply found"}, nil
}

func (n *Notion) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	return surface.ErrUnsupported
}

func (n *Notion) GoalSet(ctx context.Context, sess *surface.Session, text string) error {
	return surface.ErrUnsupported
}

func (n *Notion) GoalClear(ctx context.Context, sess *surface.Session) error {
	return surface.ErrUnsupported
}

func (n *Notion) GoalGet(ctx context.Context, sess *surface.Session) (*surface.GoalState, error) {
	return nil, nil
}

func (n *Notion) Compact(ctx context.Context, sess *surface.Session) error {
	return surface.ErrUnsupported
}

func (n *Notion) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	n.ensureContext()
	if name == "" {
		body, _ := json.Marshal(map[string]any{"spaceId": n.spaceID})
		status, respBody, err := sidecarPostWithCookies(
			n.inferenceURL("getAvailableModels"),
			n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
		if err != nil {
			return "", fmt.Errorf("get models: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("get models (HTTP %d): %s", status, respBody)
		}
		var resp struct {
			Models []struct {
				Model        string `json:"model"`
				ModelMessage string `json:"modelMessage"`
				Family       string `json:"modelFamily"`
			} `json:"models"`
		}
		if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
			return "", fmt.Errorf("parse models: %w", err)
		}
		var sb strings.Builder
		for _, m := range resp.Models {
			fmt.Fprintf(&sb, "%s = %q [%s]\n", m.Model, m.ModelMessage, m.Family)
		}
		return sb.String(), nil
	}
	return fmt.Sprintf("model selection is per-request; pass model name in send config (current: %s)", name), nil
}

func (n *Notion) Interrupt(ctx context.Context, sess *surface.Session) error {
	return surface.ErrUnsupported
}

func (n *Notion) Steer(ctx context.Context, sess *surface.Session, message string) error {
	_, err := n.Send(ctx, sess, message)
	return err
}

func parseNotionResponse(ndjson string) string {
	lines := strings.Split(strings.TrimSpace(ndjson), "\n")
	var content string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}

		switch obj["type"] {
		case "agent-inference":
			if obj["finishedAt"] != nil {
				if values, ok := obj["value"].([]any); ok {
					for _, v := range values {
						if m, ok := v.(map[string]any); ok && m["type"] == "text" {
							if c, ok := m["content"].(string); ok {
								content = c
							}
						}
					}
				}
			}
		case "patch":
			if ops, ok := obj["v"].([]any); ok {
				for _, op := range ops {
					opMap, ok := op.(map[string]any)
					if !ok {
						continue
					}
					p, _ := opMap["p"].(string)
					if !strings.Contains(p, "/content") {
						continue
					}
					switch opMap["o"] {
					case "x": // append
						if v, ok := opMap["v"].(string); ok {
							content += v
						}
					case "p": // replace
						if v, ok := opMap["v"].(string); ok {
							content = v
						}
					}
				}
			}
		}
	}

	return langTagRe.ReplaceAllString(content, "")
}

func extractThreadID(ndjson string) string {
	lines := strings.Split(strings.TrimSpace(ndjson), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			continue
		}
		if obj["type"] != "record-map" {
			continue
		}
		if rm, ok := obj["recordMap"].(map[string]any); ok {
			if threads, ok := rm["thread"].(map[string]any); ok {
				for k := range threads {
					return k
				}
			}
		}
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (n *Notion) Tail(ctx context.Context, sess *surface.Session, msgCount int) ([]surface.Exchange, error) {
	n.ensureContext()
	if sess.ID == "" {
		return nil, fmt.Errorf("notion tail: no thread ID")
	}

	body, _ := json.Marshal(map[string]any{
		"requests": []map[string]any{{
			"pointer": map[string]any{"table": "thread", "id": sess.ID},
			"version": -1,
		}},
	})
	status, respBody, err := sidecarPostWithCookies(
		n.inferenceURL("syncRecordValues"),
		n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("notion tail: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("notion tail (HTTP %d)", status)
	}
	var threadResp struct {
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
	if err := json.Unmarshal([]byte(respBody), &threadResp); err != nil {
		return nil, fmt.Errorf("notion tail: parse thread: %w", err)
	}
	threadRec, ok := threadResp.RecordMap.Thread[sess.ID]
	if !ok || len(threadRec.Value.Value.Messages) == 0 {
		return nil, fmt.Errorf("notion tail: no messages")
	}
	msgIDs := threadRec.Value.Value.Messages
	if len(msgIDs) > 20 {
		msgIDs = msgIDs[len(msgIDs)-20:]
	}

	reqs := make([]map[string]any, len(msgIDs))
	for i, id := range msgIDs {
		reqs[i] = map[string]any{
			"pointer": map[string]any{"table": "thread_message", "id": id},
			"version": -1,
		}
	}
	body2, _ := json.Marshal(map[string]any{"requests": reqs})
	status2, respBody2, err := sidecarPostWithCookies(
		n.inferenceURL("syncRecordValues"),
		n.headers(), string(body2), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("notion tail: fetch messages: %w", err)
	}
	if status2 != 200 {
		return nil, fmt.Errorf("notion tail fetch (HTTP %d)", status2)
	}

	var msgResp struct {
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
	if err := json.Unmarshal([]byte(respBody2), &msgResp); err != nil {
		return nil, fmt.Errorf("notion tail: parse messages: %w", err)
	}

	var exchanges []surface.Exchange
	for _, mid := range msgIDs {
		rec, ok := msgResp.RecordMap.ThreadMessage[mid]
		if !ok {
			continue
		}
		step := rec.Value.Value.Step
		if step.Type == "user" {
			var items [][]string
			if json.Unmarshal(step.Value, &items) == nil && len(items) > 0 && len(items[0]) > 0 {
				text := strings.TrimSpace(items[0][0])
				if text != "" {
					if len(exchanges) == 0 || exchanges[len(exchanges)-1].Assistant != "" {
						exchanges = append(exchanges, surface.Exchange{User: text})
					} else {
						exchanges[len(exchanges)-1].User = text
					}
				}
			}
		} else if step.Type == "agent-inference" {
			var items []struct {
				Type    string `json:"type"`
				Content string `json:"content"`
			}
			if json.Unmarshal(step.Value, &items) != nil {
				continue
			}
			for _, item := range items {
				if item.Type == "text" && item.Content != "" {
					text := langTagRe.ReplaceAllString(item.Content, "")
					text = strings.TrimSpace(text)
					if text != "" {
						if len(exchanges) == 0 {
							exchanges = append(exchanges, surface.Exchange{Assistant: text})
						} else if exchanges[len(exchanges)-1].Assistant == "" {
							exchanges[len(exchanges)-1].Assistant = text
						} else {
							exchanges = append(exchanges, surface.Exchange{Assistant: text})
						}
					}
				}
			}
		}
	}

	if len(exchanges) > msgCount {
		exchanges = exchanges[len(exchanges)-msgCount:]
	}
	return exchanges, nil
}
