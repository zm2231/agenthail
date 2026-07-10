package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

var langTagRe = regexp.MustCompile(`(?i)^<lang[^>]*/?>\s*`)

type Notion struct {
	spaceID      string
	userID       string
	userName     string
	timezone     string
	cookieBridge string
	modelMu      sync.Mutex
	modelLabels  map[string]string
	modelsAt     time.Time
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

func (n *Notion) ensureContext(ctx context.Context) error {
	if n.spaceID == "" || n.userID == "" {
		if err := n.autoDetect(ctx); err != nil {
			return err
		}
	}
	canonical, err := canonicalNotionSpaceID(n.spaceID)
	if err != nil {
		return err
	}
	n.spaceID = canonical
	return nil
}

func canonicalNotionSpaceID(spaceID string) (string, error) {
	parsed, err := uuid.Parse(spaceID)
	if err != nil {
		return "", fmt.Errorf("AGENTHAIL_NOTION_SPACE must be a UUID (got %q)", spaceID)
	}
	return parsed.String(), nil
}

func newNotionThreadID(spaceID string) (string, error) {
	canonical, err := canonicalNotionSpaceID(spaceID)
	if err != nil {
		return "", err
	}
	spaceParts := strings.Split(canonical, "-")
	parts := strings.Split(uuid.NewString(), "-")
	if len(spaceParts) != 5 || len(parts) != 5 {
		return "", fmt.Errorf("generate Notion thread ID: invalid UUID segment count")
	}
	parts[1] = spaceParts[1]
	threadID := strings.Join(parts, "-")
	if _, err := uuid.Parse(threadID); err != nil {
		return "", fmt.Errorf("generate Notion thread ID: %w", err)
	}
	return threadID, nil
}

func (n *Notion) autoDetect(ctx context.Context) error {
	status, respBody, err := sidecarRequestWithCookies(ctx, "POST",
		"https://app.notion.com/api/v3/getSpaces",
		n.headers(), "{}", n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return fmt.Errorf("detect Notion context: %w", err)
	}
	if status != 200 {
		return fmt.Errorf("detect Notion context (HTTP %d)", status)
	}
	var resp map[string]any
	if json.Unmarshal([]byte(respBody), &resp) != nil {
		return fmt.Errorf("parse Notion context")
	}
	var users []string
	for uid, udata := range resp {
		if uid == "isNotionError" || uid == "errorId" || uid == "name" || uid == "debugMessage" || uid == "message" {
			continue
		}
		if _, ok := udata.(map[string]any); ok {
			users = append(users, uid)
		}
	}
	sort.Strings(users)
	if n.userID == "" {
		if len(users) == 0 {
			return fmt.Errorf("no Notion user found; set AGENTHAIL_NOTION_USER")
		}
		if len(users) > 1 {
			return fmt.Errorf("multiple Notion users found; set AGENTHAIL_NOTION_USER")
		}
		n.userID = users[0]
	}
	udataMap, ok := resp[n.userID].(map[string]any)
	if !ok {
		return fmt.Errorf("Notion user %q not found", n.userID)
	}
	uid := n.userID
	if n.userName == "" {
		if usersByID, ok := udataMap["notion_user"].(map[string]any); ok {
			if record, ok := usersByID[uid].(map[string]any); ok {
				if value, ok := record["value"].(map[string]any); ok {
					if inner, ok := value["value"].(map[string]any); ok {
						n.userName, _ = inner["name"].(string)
					}
				}
			}
		}
	}
	if n.timezone == "" {
		if settingsByID, ok := udataMap["user_settings"].(map[string]any); ok {
			if record, ok := settingsByID[uid].(map[string]any); ok {
				if value, ok := record["value"].(map[string]any); ok {
					if inner, ok := value["value"].(map[string]any); ok {
						if settings, ok := inner["settings"].(map[string]any); ok {
							n.timezone, _ = settings["time_zone"].(string)
						}
					}
				}
			}
		}
	}
	if spaces, ok := udataMap["space"].(map[string]any); ok && n.spaceID == "" {
		var ids []string
		for sid := range spaces {
			ids = append(ids, sid)
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			return fmt.Errorf("no Notion space found; set AGENTHAIL_NOTION_SPACE")
		}
		if len(ids) > 1 {
			return fmt.Errorf("multiple Notion spaces found; set AGENTHAIL_NOTION_SPACE")
		}
		n.spaceID = ids[0]
	}
	if n.spaceID == "" {
		return fmt.Errorf("Notion space unavailable; set AGENTHAIL_NOTION_SPACE")
	}
	return nil
}

func (n *Notion) Name() surface.SurfaceKind { return surface.KindNotion }

func (n *Notion) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: false, Reply: true,
		Interrupt: false, Steer: false, Model: false,
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
	if err := n.ensureContext(ctx); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"threadParentPointer": map[string]any{
			"table":   "space",
			"id":      n.spaceID,
			"spaceId": n.spaceID,
		},
		"limit":              50,
		"includeWriterChats": false,
	})
	status, respBody, err := sidecarRequestWithCookies(ctx, "POST",
		n.inferenceURL("getInferenceTranscriptsForUser"),
		n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("list threads: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("list threads (HTTP %d): %s", status, diagnosticExcerpt(respBody))
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
	if target == "new" || strings.HasPrefix(target, "new:") {
		name := strings.TrimPrefix(target, "new:")
		return &surface.Session{ID: target, Surface: surface.KindNotion, Name: name, Status: surface.StatusIdle}, nil
	}
	if looksLikeUUID(target) {
		exists, err := n.threadExists(ctx, target)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("Notion thread %s not found", target)
		}
		return &surface.Session{ID: target, Surface: surface.KindNotion, Status: surface.StatusIdle}, nil
	}
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
	return n.SendWithOptions(ctx, sess, message, surface.SendOptions{})
}

var notionInferenceRequest = sidecarRequestWithCookies

func (n *Notion) SendWithOptions(ctx context.Context, sess *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	if err := n.ensureContext(ctx); err != nil {
		return nil, err
	}
	baselineID, _, err := n.latestAgentReply(ctx, sess)
	if err != nil {
		return nil, fmt.Errorf("notion send target: %w", err)
	}
	model := options.Model
	if model == "" {
		model = "almond-croissant-low"
	} else {
		resolved, rErr := n.resolveModelCodename(ctx, model)
		if rErr != nil {
			return nil, rErr
		}
		model = resolved
	}
	if err := n.fetchModels(ctx); err != nil {
		return nil, err
	}
	n.modelMu.Lock()
	if _, ok := n.modelLabels[model]; !ok {
		n.modelMu.Unlock()
		return nil, fmt.Errorf("Notion model %q is not available for this workspace", model)
	}
	n.modelMu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	newThread := sess.ID == "" || strings.HasPrefix(sess.ID, "new")
	threadID := sess.ID
	if newThread {
		threadID, err = newNotionThreadID(n.spaceID)
		if err != nil {
			return nil, err
		}
	}

	transcript := []map[string]any{
		{
			"id":   uuid.NewString(),
			"type": "config",
			"value": map[string]any{
				"type":                     "workflow",
				"model":                    model,
				"modelFromUser":            true,
				"isCustomAgent":            false,
				"useWebSearch":             true,
				"enableCustomAgents":       false,
				"enableCreateAndRunThread": true,
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
				"surface":         "ai_module",
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
		"traceId":                       uuid.NewString(),
		"spaceId":                       n.spaceID,
		"transcript":                    transcript,
		"threadId":                      threadID,
		"createThread":                  newThread,
		"generateTitle":                 newThread,
		"saveAllThreadOperations":       true,
		"setUnreadState":                true,
		"createdSource":                 "ai_module",
		"threadType":                    "workflow",
		"isPartialTranscript":           false,
		"asPatchResponse":               true,
		"patchResponseVersion":          2,
		"isUserInAnySalesAssistedSpace": false,
		"isSpaceSalesAssisted":          false,
	}
	if newThread {
		reqBody["threadParentPointer"] = map[string]any{
			"table":   "space",
			"id":      n.spaceID,
			"spaceId": n.spaceID,
		}
	}
	bodyBytes, _ := json.Marshal(reqBody)
	headers := n.headers()
	headers["accept"] = "application/x-ndjson"

	status, respBody, err := notionInferenceRequest(ctx, "POST",
		n.inferenceURL("runInferenceTranscript"),
		headers, string(bodyBytes), n.bridge(), "https://app.notion.com/", 60*time.Second)
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("notion send: %w", err))
	}
	if status != 200 {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("notion send (HTTP %d): %s", status, diagnosticExcerpt(respBody)))
	}
	if err := validateNotionInferenceResponse(respBody); err != nil {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("notion inference: %w", err))
	}

	resultThreadID := threadID
	if resultThreadID == "" {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Notion send succeeded without a thread id"))
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		completionID, reply, observeErr := n.latestAgentReply(ctx, &surface.Session{ID: resultThreadID, Surface: surface.KindNotion})
		if observeErr != nil {
			return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("confirm Notion persistence: %w", observeErr))
		}
		if completionID != "" && completionID != baselineID && reply != nil && reply.Done {
			return &surface.SendResult{UUID: resultThreadID, Accepted: true}, nil
		}
		select {
		case <-ctx.Done():
			return nil, surface.DeliveryOutcomeUnknown(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Notion inference completed but no new persisted agent message appeared within 10s"))
}

func (n *Notion) fetchModels(ctx context.Context) error {
	if err := n.ensureContext(ctx); err != nil {
		return err
	}
	n.modelMu.Lock()
	defer n.modelMu.Unlock()
	if n.modelLabels != nil && time.Since(n.modelsAt) < 5*time.Minute {
		return nil
	}
	body, _ := json.Marshal(map[string]any{"spaceId": n.spaceID})
	status, responseBody, err := sidecarRequestWithCookies(ctx, "POST", n.inferenceURL("getAvailableModels"), n.headers(), string(body), n.bridge(), "https://app.notion.com/", 15*time.Second)
	if err != nil {
		return fmt.Errorf("list Notion models: %w", err)
	}
	if status != 200 {
		return fmt.Errorf("list Notion models (HTTP %d): %s", status, diagnosticExcerpt(responseBody))
	}
	var response struct {
		Models []struct {
			Model      string `json:"model"`
			Label      string `json:"modelMessage"`
			IsDisabled bool   `json:"isDisabled"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(responseBody), &response); err != nil {
		return fmt.Errorf("parse Notion models: %w", err)
	}
	n.modelLabels = make(map[string]string, len(response.Models))
	for _, entry := range response.Models {
		if entry.Model != "" && !entry.IsDisabled {
			n.modelLabels[entry.Model] = entry.Label
		}
	}
	n.modelsAt = time.Now()
	return nil
}

func (n *Notion) resolveModelCodename(ctx context.Context, name string) (string, error) {
	if err := n.fetchModels(ctx); err != nil {
		return "", err
	}
	n.modelMu.Lock()
	defer n.modelMu.Unlock()
	if _, ok := n.modelLabels[name]; ok {
		return name, nil
	}
	for codename, label := range n.modelLabels {
		if strings.EqualFold(label, name) {
			return codename, nil
		}
	}
	return "", fmt.Errorf("Notion model %q not found; use an available codename or label with 'agenthail send <notion-target> --model <name>'", name)
}

func validateNotionInferenceResponse(ndjson string) error {
	recordCount := 0
	for _, line := range strings.Split(ndjson, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return fmt.Errorf("invalid NDJSON record: %w", err)
		}
		recordCount++
		typeName, _ := record["type"].(string)
		if typeName == "error" || record["isNotionError"] == true || record["error"] != nil {
			return fmt.Errorf("upstream error: %s", diagnosticExcerpt(line))
		}
	}
	if recordCount == 0 {
		return fmt.Errorf("response contained no NDJSON records")
	}
	return nil
}

func (n *Notion) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	_, reply, err := n.latestAgentReply(ctx, sess)
	if err != nil {
		return nil, err
	}
	if reply == nil {
		return &surface.ReplyResult{Done: false}, nil
	}
	return reply, nil
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
	return "", surface.ErrUnsupported
}

func (n *Notion) Interrupt(ctx context.Context, sess *surface.Session) error {
	return surface.ErrUnsupported
}

func (n *Notion) Steer(ctx context.Context, sess *surface.Session, message string) error {
	return surface.ErrUnsupported
}

func (n *Notion) Tail(ctx context.Context, sess *surface.Session, msgCount int) ([]surface.Exchange, error) {
	if err := n.ensureContext(ctx); err != nil {
		return nil, err
	}
	if sess.ID == "" {
		return nil, fmt.Errorf("notion tail: no thread ID")
	}

	body, _ := json.Marshal(map[string]any{
		"requests": []map[string]any{{
			"pointer": map[string]any{"table": "thread", "id": sess.ID},
			"version": -1,
		}},
	})
	status, respBody, err := sidecarRequestWithCookies(ctx, "POST",
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

	reqs := make([]map[string]any, len(msgIDs))
	for i, id := range msgIDs {
		reqs[i] = map[string]any{
			"pointer": map[string]any{"table": "thread_message", "id": id},
			"version": -1,
		}
	}
	body2, _ := json.Marshal(map[string]any{"requests": reqs})
	status2, respBody2, err := sidecarRequestWithCookies(ctx, "POST",
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
