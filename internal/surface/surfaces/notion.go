package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

// Notion drives Notion's default AI chat via the internal API (runInferenceTranscript).
// Requests go through the curl_cffi sidecar to bypass Cloudflare.
// Domain migrated from www.notion.so to app.notion.com.

var langTagRe = regexp.MustCompile(`(?i)^<lang[^>]*/?>\s*`)

type Notion struct {
	spaceID     string
	userID      string
	cookieBridge string
}

func NewNotion(spaceID, userID string) *Notion {
	return &Notion{
		spaceID: spaceID,
		userID: userID,
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
		Send: true, Stream: true, Reply: true,
		Interrupt: true, Steer: true, Model: true,
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

// --- Session model ---
//
// Notion AI threads are flat: each thread is a conversation.  We expose them as
// sessions where ID = thread ID.  "HasLocal" is false (no local transcript).

func (n *Notion) List(ctx context.Context) ([]surface.Session, error) {
	n.ensureContext()
	body, _ := json.Marshal(map[string]any{
		"threadParentPointer": map[string]any{
			"table":   "space",
			"id":      n.spaceID,
			"spaceId": n.spaceID,
		},
		"limit":               50,
		"includeWriterChats":   false,
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
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"transcripts"`
	}
	if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
		return nil, fmt.Errorf("parse threads: %w", err)
	}

	sessions := make([]surface.Session, 0, len(resp.Transcripts))
	for _, t := range resp.Transcripts {
		sessions = append(sessions, surface.Session{
			ID:      t.ID,
			Surface: surface.KindNotion,
			Name:    t.Title,
			Status:  surface.StatusIdle,
		})
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

// --- Send ---

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
				"enableAgentIntegrations":  false,
			},
		},
		{
			"id":   uuid.NewString(),
			"type": "context",
			"value": map[string]any{
				"timezone":        "America/Chicago",
				"userName":        "Zain",
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

	// Extract thread ID from response
	resultThreadID := threadID
	content := parseNotionResponse(respBody)
	if resultThreadID == "" {
		resultThreadID = extractThreadID(respBody)
	}

	_ = content // reply is fetched separately

	return &surface.SendResult{UUID: resultThreadID, Accepted: true}, nil
}

// --- Reply ---

func (n *Notion) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	n.ensureContext()
	if sess.ID == "" {
		return &surface.ReplyResult{Error: "no thread ID"}, nil
	}

	// 1. Get thread record to find message IDs
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
	// Notion's syncRecordValues has a ~20 request limit per call
	if len(msgIDs) > 20 {
		msgIDs = msgIDs[len(msgIDs)-20:]
	}

	// 2. Fetch message records
	reqs := make([]map[string]any, len(msgIDs))
	for i, id := range msgIDs {
		reqs[i] = map[string]any{
			"pointer":  map[string]any{"table": "thread_message", "id": id},
			"version":  -1,
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

	// Find the last assistant text
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
	

	// Iterate message IDs in reverse to find last assistant text
	
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

// --- Stream ---

func (n *Notion) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	// Not implemented yet — would require streaming the NDJSON response
	return surface.ErrUnsupported
}

// --- Goal / Compact (unsupported) ---

func (n *Notion) GoalSet(ctx context.Context, sess *surface.Session, text string) error {
	return surface.ErrUnsupported
}

func (n *Notion) GoalClear(ctx context.Context, sess *surface.Session) error {
	return surface.ErrUnsupported
}

func (n *Notion) Compact(ctx context.Context, sess *surface.Session) error {
	return surface.ErrUnsupported
}

// --- Model ---

func (n *Notion) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	n.ensureContext()
	if name == "" {
		// List available models
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
	// Setting model per-thread isn't supported via this API (model is per-request)
	return fmt.Sprintf("model selection is per-request; pass model name in send config (current: %s)", name), nil
}

// --- Interrupt ---

func (n *Notion) Interrupt(ctx context.Context, sess *surface.Session) error {
	// Notion AI doesn't have a separate interrupt endpoint; the front-end
	// sends a saveTransactionsFanout or stops the fetch stream. We return
	// unsupported for now.
	return surface.ErrUnsupported
}

// --- Steer ---

func (n *Notion) Steer(ctx context.Context, sess *surface.Session, message string) error {
	// Notion has no mid-turn steer like Codex. Steer = send a follow-up message.
	_, err := n.Send(ctx, sess, message)
	return err
}

// --- Helpers ---

// parseNotionResponse extracts text content from an NDJSON response.
// Handles both agent-inference chunks (new thread) and patch ops (follow-up).
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
			// Follow-up responses stream via patch ops
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

// max returns the larger of two ints (Go 1.21+ has builtin but keeping for safety)
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}