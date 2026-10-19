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
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

var langTagRe = regexp.MustCompile(`(?i)^<lang[^>]*/?>\s*`)

type Notion struct {
	signins            signin.Resolver
	configuredSpace    string
	configuredUser     string
	configuredTimezone string
	cookieBridge       string
	accountMu          sync.Mutex
	account            *notionAccount
	modelMu            sync.Mutex
	models             *notionModels
}

// notionAccount is the Notion identity one operation runs as: the cookies of
// one sign-in profile and the user and space detected through them. It is
// never mutated, so a binding change cannot mix two accounts in one request.
type notionAccount struct {
	cookies  CookieSource
	spaceID  string
	userID   string
	userName string
	timezone string
}

type notionModels struct {
	profileID string
	labels    map[string]string
	at        time.Time
}

func NewNotion(signins signin.Resolver, spaceID, userID string) *Notion {
	return &Notion{
		signins:            signins,
		configuredSpace:    spaceID,
		configuredUser:     userID,
		configuredTimezone: os.Getenv("AGENTHAIL_NOTION_TZ"),
		cookieBridge:       configuredCookieBridge(),
	}
}

func (n *Notion) cookies() (CookieSource, error) {
	source, err := signin.Resolve(n.signins, signin.Target{Consumer: signin.Notion})
	if err != nil {
		return CookieSource{}, err
	}
	return CookieSource{Bridge: n.cookieBridge, URL: "https://app.notion.com/", Signin: source}, nil
}

// currentAccount resolves the sign-in profile, then reuses the account
// detected for that same profile or detects it again.
func (n *Notion) currentAccount(ctx context.Context) (notionAccount, error) {
	cookies, err := n.cookies()
	if err != nil {
		return notionAccount{}, err
	}
	return n.accountFor(ctx, cookies)
}

func (n *Notion) accountFor(ctx context.Context, cookies CookieSource) (notionAccount, error) {
	n.accountMu.Lock()
	cached := n.account
	n.accountMu.Unlock()
	if cached != nil && cached.cookies == cookies {
		return *cached, nil
	}
	account := notionAccount{cookies: cookies, spaceID: n.configuredSpace, userID: n.configuredUser, timezone: n.configuredTimezone}
	if account.spaceID == "" || account.userID == "" {
		if err := account.detect(ctx); err != nil {
			return notionAccount{}, err
		}
	}
	canonical, err := canonicalNotionSpaceID(account.spaceID)
	if err != nil {
		return notionAccount{}, err
	}
	account.spaceID = canonical
	n.accountMu.Lock()
	n.account = &account
	n.accountMu.Unlock()
	return account, nil
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

func (a *notionAccount) detect(ctx context.Context) error {
	status, respBody, err := sidecarRequestWithCookies(ctx, "POST",
		"https://app.notion.com/api/v3/getSpaces",
		a.headers(), "{}", a.cookies, 15*time.Second)
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
	if a.userID == "" {
		if len(users) == 0 {
			return fmt.Errorf("no Notion user found; set AGENTHAIL_NOTION_USER")
		}
		if len(users) > 1 {
			return fmt.Errorf("multiple Notion users found; set AGENTHAIL_NOTION_USER")
		}
		a.userID = users[0]
	}
	udataMap, ok := resp[a.userID].(map[string]any)
	if !ok {
		return fmt.Errorf("Notion user %q not found", a.userID)
	}
	uid := a.userID
	if a.userName == "" {
		if usersByID, ok := udataMap["notion_user"].(map[string]any); ok {
			if record, ok := usersByID[uid].(map[string]any); ok {
				if value, ok := record["value"].(map[string]any); ok {
					if inner, ok := value["value"].(map[string]any); ok {
						a.userName, _ = inner["name"].(string)
					}
				}
			}
		}
	}
	if a.timezone == "" {
		if settingsByID, ok := udataMap["user_settings"].(map[string]any); ok {
			if record, ok := settingsByID[uid].(map[string]any); ok {
				if value, ok := record["value"].(map[string]any); ok {
					if inner, ok := value["value"].(map[string]any); ok {
						if settings, ok := inner["settings"].(map[string]any); ok {
							a.timezone, _ = settings["time_zone"].(string)
						}
					}
				}
			}
		}
	}
	if spaces, ok := udataMap["space"].(map[string]any); ok && a.spaceID == "" {
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
		a.spaceID = ids[0]
	}
	if a.spaceID == "" {
		return fmt.Errorf("Notion space unavailable; set AGENTHAIL_NOTION_SPACE")
	}
	return nil
}

func (n *Notion) Name() surface.SurfaceKind { return surface.KindNotion }

func (n *Notion) RuntimeKinds() []surface.RuntimeKind {
	return []surface.RuntimeKind{surface.RuntimeNotionWeb}
}

func (n *Notion) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: false, Reply: true,
		Interrupt: false, Steer: false, Model: false,
	}
}

func (n *Notion) CatalogListComplete() bool { return false }

func (a notionAccount) headers() map[string]string {
	return map[string]string{
		"content-type":                "application/json",
		"x-notion-active-user-header": a.userID,
		"x-notion-space-id":           a.spaceID,
	}
}

func notionAPI(action string) string {
	return "https://app.notion.com/api/v3/" + action
}

func (n *Notion) List(ctx context.Context) ([]surface.Session, error) {
	account, err := n.currentAccount(ctx)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"threadParentPointer": map[string]any{
			"table":   "space",
			"id":      account.spaceID,
			"spaceId": account.spaceID,
		},
		"limit":              50,
		"includeWriterChats": false,
	})
	status, respBody, err := sidecarRequestWithCookies(ctx, "POST",
		notionAPI("getInferenceTranscriptsForUser"),
		account.headers(), string(body), account.cookies, 15*time.Second)
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
			ID:          t.ID,
			Surface:     surface.KindNotion,
			RuntimeKind: surface.RuntimeNotionWeb,
			Name:        t.Title,
			Status:      surface.StatusIdle,
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
		return &surface.Session{ID: target, Surface: surface.KindNotion, RuntimeKind: surface.RuntimeNotionWeb, Name: name, Status: surface.StatusIdle}, nil
	}
	if looksLikeUUID(target) {
		exists, err := n.threadExists(ctx, target)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("Notion thread %s not found", target)
		}
		return &surface.Session{ID: target, Surface: surface.KindNotion, RuntimeKind: surface.RuntimeNotionWeb, Status: surface.StatusIdle}, nil
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
	cookies, err := n.cookies()
	if err != nil {
		return nil, surface.DeliveryTerminal(err, signinTerminalKind(err))
	}
	account, err := n.accountFor(ctx, cookies)
	if err != nil {
		return nil, err
	}
	baselineID, _, err := account.latestAgentReply(ctx, sess)
	if err != nil {
		return nil, fmt.Errorf("notion send target: %w", err)
	}
	model := options.Model
	if model == "" {
		model = "almond-croissant-low"
	} else {
		resolved, rErr := n.resolveModelCodename(ctx, account, model)
		if rErr != nil {
			return nil, rErr
		}
		model = resolved
	}
	labels, err := n.modelLabels(ctx, account)
	if err != nil {
		return nil, err
	}
	if _, ok := labels[model]; !ok {
		return nil, fmt.Errorf("Notion model %q is not available for this workspace", model)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	newThread := sess.ID == "" || strings.HasPrefix(sess.ID, "new")
	threadID := sess.ID
	if newThread {
		threadID, err = newNotionThreadID(account.spaceID)
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
				"timezone":        account.timezone,
				"userName":        account.userName,
				"userId":          account.userID,
				"spaceId":         account.spaceID,
				"currentDatetime": now,
				"surface":         "ai_module",
			},
		},
		{
			"id":        uuid.NewString(),
			"type":      "user",
			"value":     [][]string{{message}},
			"userId":    account.userID,
			"createdAt": now,
		},
	}

	reqBody := map[string]any{
		"traceId":                       uuid.NewString(),
		"spaceId":                       account.spaceID,
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
			"id":      account.spaceID,
			"spaceId": account.spaceID,
		}
	}
	bodyBytes, _ := json.Marshal(reqBody)
	headers := account.headers()
	headers["accept"] = "application/x-ndjson"

	status, respBody, err := notionInferenceRequest(ctx, "POST",
		notionAPI("runInferenceTranscript"),
		headers, string(bodyBytes), account.cookies, 60*time.Second)
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
		completionID, reply, observeErr := account.latestAgentReply(ctx, &surface.Session{ID: resultThreadID, Surface: surface.KindNotion})
		if observeErr != nil {
			return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("confirm Notion persistence: %w", observeErr))
		}
		if completionID != "" && completionID != baselineID && reply != nil && reply.Done {
			if newThread {
				return &surface.SendResult{UUID: resultThreadID, Accepted: true}, nil
			}
			return &surface.SendResult{UUID: completionID, Accepted: true}, nil
		}
		select {
		case <-ctx.Done():
			return nil, surface.DeliveryOutcomeUnknown(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("Notion inference completed but no new persisted agent message appeared within 10s"))
}

// modelLabels returns the models available to the account's workspace. The
// returned map is never modified afterwards.
func (n *Notion) modelLabels(ctx context.Context, account notionAccount) (map[string]string, error) {
	n.modelMu.Lock()
	cached := n.models
	n.modelMu.Unlock()
	if cached != nil && cached.profileID == account.cookies.Signin.ProfileID && time.Since(cached.at) < 5*time.Minute {
		return cached.labels, nil
	}
	body, _ := json.Marshal(map[string]any{"spaceId": account.spaceID})
	status, responseBody, err := sidecarRequestWithCookies(ctx, "POST", notionAPI("getAvailableModels"), account.headers(), string(body), account.cookies, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("list Notion models: %w", err)
	}
	if status != 200 {
		return nil, fmt.Errorf("list Notion models (HTTP %d): %s", status, diagnosticExcerpt(responseBody))
	}
	var response struct {
		Models []struct {
			Model      string `json:"model"`
			Label      string `json:"modelMessage"`
			IsDisabled bool   `json:"isDisabled"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(responseBody), &response); err != nil {
		return nil, fmt.Errorf("parse Notion models: %w", err)
	}
	labels := make(map[string]string, len(response.Models))
	for _, entry := range response.Models {
		if entry.Model != "" && !entry.IsDisabled {
			labels[entry.Model] = entry.Label
		}
	}
	n.modelMu.Lock()
	n.models = &notionModels{profileID: account.cookies.Signin.ProfileID, labels: labels, at: time.Now()}
	n.modelMu.Unlock()
	return labels, nil
}

func (n *Notion) resolveModelCodename(ctx context.Context, account notionAccount, name string) (string, error) {
	labels, err := n.modelLabels(ctx, account)
	if err != nil {
		return "", err
	}
	if _, ok := labels[name]; ok {
		return name, nil
	}
	for codename, label := range labels {
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
	read, err := n.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: msgCount})
	if err != nil {
		return nil, err
	}
	return read.Exchanges, nil
}
