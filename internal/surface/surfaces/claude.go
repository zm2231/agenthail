package surfaces

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type Claude struct {
	profile      string
	home         string
	cookieBridge string
	contextMu    sync.Mutex
	contextState map[string]*claudeContextState
}

func NewClaude(profile, home string) *Claude {
	if profile == "" {
		profile = "Default"
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	bridge := os.Getenv("AGENTHAIL_COOKIE_BRIDGE")
	if bridge == "" {
		bridge = cookieBridgePath("cookie")
	}
	return &Claude{profile: profile, home: home, cookieBridge: bridge}
}

func (c *Claude) Name() surface.SurfaceKind { return surface.KindClaude }

func (c *Claude) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: true, Reply: true, Goal: false,
		Compact: true, Model: true, Interrupt: true, Steer: true,
	}
}

func (c *Claude) Health(ctx context.Context) error {
	if _, err := sidecarPath(); err != nil {
		return err
	}
	if c.cookieBridge == "" || !fileExists(c.cookieBridge) {
		return fmt.Errorf("Claude cookie bridge not found (set AGENTHAIL_COOKIE_BRIDGE or install cookie.mjs alongside agenthail)")
	}
	status, body, err := sidecarRequestWithCookies(ctx, "GET", "https://claude.ai/api/organizations", c.headerMap("", ""), "", c.cookieBridge, "https://claude.ai/", 15*time.Second)
	if err != nil {
		return fmt.Errorf("Claude authentication probe: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("Claude authentication probe returned HTTP %d: %s", status, diagnosticExcerpt(body))
	}
	return nil
}

func (c *Claude) headerMap(_, _ string) map[string]string {
	return map[string]string{
		"user-agent":                chromeUA,
		"accept":                    "application/json",
		"accept-language":           "en-US,en;q=0.9",
		"origin":                    "https://claude.ai",
		"referer":                   "https://claude.ai/code/",
		"anthropic-version":         "2023-06-01",
		"anthropic-beta":            "ccr-byoc-2025-07-29",
		"anthropic-client-platform": "web_claude_ai",
		"anthropic-client-feature":  "ccr",
		"anthropic-client-version":  "1.0.0",
	}
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]), hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]), hex.EncodeToString(b[10:16]))
}

const alphanum = "abcdefghijklmnopqrstuvwxyz0123456789"

func randSeq(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = alphanum[int(b[i])%len(alphanum)]
	}
	return string(b)
}

func toCse(bridgeID string) string {
	s := bridgeID
	s = strings.TrimPrefix(s, "https://claude.ai/code/")
	s = strings.TrimPrefix(s, "session_")
	s = strings.TrimPrefix(s, "cse_")
	return "cse_" + s
}

func projectDir(cwd string) string {
	if cwd == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects", strings.ReplaceAll(cwd, "/", "-"))
}

func (c *Claude) transcriptPath(s *surface.Session) string {
	return c.resolveTranscript(s, s.ID)
}

func (c *Claude) resolveTranscript(s *surface.Session, conversationID string) string {
	if s.Cwd == "" || conversationID == "" {
		return ""
	}
	return filepath.Join(projectDir(s.Cwd), conversationID+".jsonl")
}

func (c *Claude) firstUserMessage(path string) string {
	if path == "" || !fileExists(path) {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for sc.Scan() {
		var e struct {
			Type string `json:"type"`
			Msg  struct {
				Content any `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if e.Type != "user" {
			continue
		}
		text := ""
		switch c := e.Msg.Content.(type) {
		case string:
			text = c
		case []any:
			for _, item := range c {
				if m, ok := item.(map[string]any); ok {
					if t, _ := m["type"].(string); t == "text" {
						if s, _ := m["text"].(string); s != "" {
							text = s
							break
						}
					}
				}
			}
		}
		if strings.HasPrefix(text, "<local-command") || strings.HasPrefix(text, "<command-") || strings.HasPrefix(text, "<system") {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			return surface.TruncateString(text, 60)
		}
	}
	return ""
}

func (c *Claude) List(ctx context.Context) ([]surface.Session, error) {
	sessionsDir := filepath.Join(c.home, ".claude", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []surface.Session{}, nil
		}
		return nil, fmt.Errorf("read Claude session directory: %w", err)
	}
	var out []surface.Session
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(sessionsDir, e.Name()))
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		bridge, _ := m["bridgeSessionId"].(string)
		if bridge == "" {
			continue
		}
		sess := surface.Session{
			ID:      bridge,
			Surface: surface.KindClaude,
			Cwd:     str(m, "cwd"),
			Status:  surface.SessionStatus(str(m, "status")),
		}
		if pid, ok := m["pid"].(float64); ok {
			sess.PID = int(pid)
		}
		if sess.PID > 0 {
			err := syscall.Kill(sess.PID, 0)
			if err != nil && !errors.Is(err, syscall.EPERM) {
				continue
			}
		}
		if n, ok := m["name"].(string); ok {
			sess.Name = n
		}
		if ts, ok := m["updatedAt"].(float64); ok && ts > 0 {
			sess.LastActive = time.UnixMilli(int64(ts))
		}
		sess.Status = claudeStatus(sess.Status)
		sess.Transcript = c.resolveTranscript(&sess, str(m, "sessionId"))
		sess.HasLocal = sess.Transcript != "" && fileExists(sess.Transcript)
		if sess.Name == "" {
			sess.Name = c.firstUserMessage(sess.Transcript)
		}
		if sess.HasLocal {
			if observation, observeErr := c.Observe(ctx, &sess); observeErr == nil && observation.Status != surface.StatusUnknown {
				sess.Status = observation.Status
			}
		}
		out = append(out, sess)
	}
	return out, nil
}

func claudeStatus(status surface.SessionStatus) surface.SessionStatus {
	switch strings.ToLower(string(status)) {
	case "idle", "shell":
		return surface.StatusIdle
	case "busy", "running", "active", "in_progress", "inprogress":
		return surface.StatusBusy
	case "offline":
		return surface.StatusOffline
	default:
		return surface.StatusUnknown
	}
}

func (c *Claude) Resolve(ctx context.Context, target string) (*surface.Session, error) {
	sessions, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(target)
	var matches []surface.Session
	for _, s := range sessions {
		if strconv.Itoa(s.PID) == target ||
			strings.HasPrefix(s.ID, target) ||
			strings.Contains(strings.ToLower(s.Cwd), lower) ||
			strings.Contains(strings.ToLower(s.Name), lower) {
			matches = append(matches, s)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no session matched '%s'", target)
	}
	if len(matches) > 1 {
		var lines []string
		for _, m := range matches {
			lines = append(lines, fmt.Sprintf("  pid=%d %s cwd=%s", m.PID, m.ID, m.Cwd))
		}
		return nil, fmt.Errorf("ambiguous target '%s':\n%s", target, strings.Join(lines, "\n"))
	}
	return &matches[0], nil
}

func (c *Claude) Observe(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	observation := &surface.TurnObservation{Status: claudeStatus(sess.Status)}
	if path == "" || !fileExists(path) {
		if observation.Status != surface.StatusOffline {
			observation.Status = surface.StatusUnknown
		}
		return observation, nil
	}
	turns, err := readClaudeTurns(path)
	if err != nil {
		return nil, err
	}
	compactPending, err := claudeCompactPending(path)
	if err != nil {
		return nil, err
	}
	if len(turns) == 0 {
		transcriptCanSetReady := observation.Status != surface.StatusOffline || claudeProcessAlive(sess.PID)
		if compactPending && transcriptCanSetReady {
			observation.Status = surface.StatusBusy
			observation.ActiveTurnID = "compact"
		} else if observation.Status != surface.StatusBusy && observation.Status != surface.StatusOffline {
			observation.Status = surface.StatusUnknown
		}
		return observation, nil
	}
	last := turns[len(turns)-1]
	transcriptCanSetReady := observation.Status != surface.StatusOffline || claudeProcessAlive(sess.PID)
	if !last.Done && !last.Interrupted && transcriptCanSetReady {
		observation.Status = surface.StatusBusy
		observation.ActiveTurnID = last.UserID
	} else if (last.Done || last.Interrupted) && transcriptCanSetReady && !claudeBridgeActivityAfterTerminal(sess, last) {
		observation.Status = surface.StatusIdle
	}
	if compactPending && transcriptCanSetReady {
		observation.Status = surface.StatusBusy
		observation.ActiveTurnID = "compact"
	}
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if !turn.Done || turn.MessageID == "" {
			continue
		}
		observation.CompletedTurnID = turn.MessageID
		observation.Reply = &surface.ReplyResult{Text: turn.Assistant, UserText: turn.User, Done: true}
		break
	}
	return observation, nil
}

func (c *Claude) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	observation, err := c.Observe(ctx, sess)
	if err != nil {
		return nil, err
	}
	if observation.Status != surface.StatusIdle || observation.ActiveTurnID != "" {
		return &surface.SendResult{UUID: sess.ID, Accepted: false}, nil
	}
	return c.postMessage(ctx, sess, message)
}

func claudeProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func claudeBridgeActivityAfterTerminal(sess *surface.Session, turn claudeTurn) bool {
	if claudeStatus(sess.Status) != surface.StatusBusy || sess.LastActive.IsZero() {
		return false
	}
	if turn.TerminalAt.IsZero() {
		return true
	}
	return sess.LastActive.After(turn.TerminalAt.Add(100 * time.Millisecond))
}

var claudeSendRequest = sidecarRequestWithCookies

func (c *Claude) postMessage(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	uuid := newUUID()
	cse := toCse(sess.ID)
	body := map[string]any{
		"events": []map[string]any{{
			"payload": map[string]any{
				"type": "user", "uuid": uuid, "session_id": sess.ID,
				"parent_tool_use_id": nil,
				"message":            map[string]any{"role": "user", "content": message},
			},
		}},
	}
	bodyBytes, _ := json.Marshal(body)
	headers := c.headerMap("", sess.ID)
	headers["content-type"] = "application/json"
	status, respBody, err := claudeSendRequest(ctx, "POST",
		"https://claude.ai/v1/code/sessions/"+cse+"/events",
		headers, string(bodyBytes), c.cookieBridge, "https://claude.ai/", 30*time.Second)
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	if strings.Contains(respBody, "Just a moment") {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("cloudflare challenge (cf_clearance may need refresh)"))
	}
	if status != 200 {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)))
	}
	return &surface.SendResult{UUID: uuid, Accepted: true}, nil
}

func (c *Claude) sendCommand(ctx context.Context, sess *surface.Session, cmd string) error {
	_, err := c.postMessage(ctx, sess, cmd)
	return err
}

func (c *Claude) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	observation, err := c.Observe(ctx, sess)
	if err != nil {
		return nil, err
	}
	if observation.Reply == nil {
		return &surface.ReplyResult{Done: false}, nil
	}
	return observation.Reply, nil
}

func (c *Claude) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	if path == "" || !fileExists(path) {
		return fmt.Errorf("no local transcript for streaming")
	}
	deadline := time.Now().Add(timeout)
	initial, err := readClaudeTurns(path)
	if err != nil {
		return err
	}
	targetID := uuid
	if targetID == "" && len(initial) > 0 && !initial[len(initial)-1].Done && !initial[len(initial)-1].Interrupted {
		targetID = initial[len(initial)-1].UserID
	}
	baseline := len(initial)
	lastText := ""
	var lastContext surface.ContextUsage
	var nextContextPoll time.Time
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !time.Now().Before(nextContextPoll) {
			nextContextPoll = time.Now().Add(time.Second)
			if usage, usageErr := c.ContextUsage(ctx, sess); usageErr == nil && usage != nil && *usage != lastContext {
				lastContext = *usage
				onEvent(surface.StreamEvent{Kind: "context", Context: usage})
			}
		}
		turns, err := readClaudeTurns(path)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if targetID == "" && len(turns) > baseline {
			targetID = turns[baseline].UserID
		}
		for _, turn := range turns {
			if targetID == "" || turn.UserID != targetID {
				continue
			}
			if turn.Assistant != "" && turn.Assistant != lastText {
				text := turn.Assistant
				if strings.HasPrefix(text, lastText) {
					text = strings.TrimPrefix(text, lastText)
				}
				lastText = turn.Assistant
				if text != "" {
					onEvent(surface.StreamEvent{Kind: "text", Text: text})
				}
			}
			if turn.Done {
				onEvent(surface.StreamEvent{Kind: "done"})
				return nil
			}
			if turn.Interrupted {
				return fmt.Errorf("Claude turn %s was interrupted", targetID)
			}
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("stream timed out after %s", timeout)
}

func (c *Claude) GoalSet(ctx context.Context, sess *surface.Session, text string) error {
	return surface.ErrUnsupported
}

func (c *Claude) GoalClear(ctx context.Context, sess *surface.Session) error {
	return surface.ErrUnsupported
}

func (c *Claude) GoalGet(ctx context.Context, sess *surface.Session) (*surface.GoalState, error) {
	return nil, surface.ErrUnsupported
}

func (c *Claude) Compact(ctx context.Context, sess *surface.Session) error {
	return c.sendCommand(ctx, sess, "/compact")
}

func (c *Claude) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	if name != "" {
		result, err := c.confirmedCommand(ctx, sess, "/model", name, 5*time.Second)
		if err != nil {
			return "", fmt.Errorf("model switch rejected: %w", err)
		}
		return result, nil
	}
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	turns, err := readClaudeTurns(path)
	if err != nil {
		return "", err
	}
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Model != "" {
			return turns[i].Model, nil
		}
	}
	return "", fmt.Errorf("model unavailable: no assistant turn recorded")
}

func (c *Claude) Models(context.Context) ([]surface.ModelOption, error) {
	return []surface.ModelOption{
		{ID: "fable", DisplayName: "Fable"},
		{ID: "opus", DisplayName: "Opus"},
		{ID: "sonnet", DisplayName: "Sonnet"},
	}, nil
}

func (c *Claude) confirmedCommand(ctx context.Context, sess *surface.Session, commandName, args string, timeout time.Duration) (string, error) {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("command confirmation requires a local transcript: %w", err)
	}
	command := commandName
	if args != "" {
		command += " " + args
	}
	if err := c.sendCommand(ctx, sess, command); err != nil {
		return "", err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("%s was accepted but no correlated confirmation appeared within %s", commandName, timeout)
		case <-ticker.C:
			result, found, readErr := readClaudeCommandResult(path, info.Size(), commandName, args)
			if readErr != nil {
				return "", fmt.Errorf("read %s confirmation: %w", commandName, readErr)
			}
			if !found {
				continue
			}
			lower := strings.ToLower(result)
			for _, failure := range []string{"not found", "error", "failed", "cannot"} {
				if strings.Contains(lower, failure) {
					return "", fmt.Errorf("%s", result)
				}
			}
			return result, nil
		}
	}
}

func (c *Claude) Interrupt(ctx context.Context, sess *surface.Session) error {
	current, err := c.Resolve(ctx, sess.ID)
	if err != nil {
		return err
	}
	if current.Status != surface.StatusBusy {
		return fmt.Errorf("session idle; nothing to interrupt")
	}
	sess = current
	cse := toCse(sess.ID)
	reqID := "interrupt-" + strconv.FormatInt(time.Now().UnixMilli(), 10) + "-" + randSeq(6)
	body := map[string]any{
		"events": []map[string]any{{
			"payload": map[string]any{
				"type":       "control_request",
				"request_id": reqID,
				"request":    map[string]any{"subtype": "interrupt"},
				"uuid":       newUUID(),
			},
		}},
	}
	bodyBytes, _ := json.Marshal(body)
	headers := c.headerMap("", sess.ID)
	headers["content-type"] = "application/json"
	status, respBody, err := sidecarRequestWithCookies(ctx, "POST",
		"https://claude.ai/v1/code/sessions/"+cse+"/events",
		headers, string(bodyBytes), c.cookieBridge, "https://claude.ai/", 10*time.Second)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("interrupt failed (HTTP %d): %s", status, diagnosticExcerpt(respBody))
	}
	return nil
}

func (c *Claude) Steer(ctx context.Context, sess *surface.Session, message string) error {
	current, err := c.Resolve(ctx, sess.ID)
	if err != nil {
		return err
	}
	if current.Status != surface.StatusBusy {
		return fmt.Errorf("session idle; nothing to steer (use 'send' instead)")
	}
	_, err = c.postMessage(ctx, current, message)
	return err
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (c *Claude) Tail(ctx context.Context, sess *surface.Session, n int) ([]surface.Exchange, error) {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	if path == "" || !fileExists(path) {
		return nil, fmt.Errorf("no local transcript")
	}
	turns, err := readClaudeTurns(path)
	if err != nil {
		return nil, err
	}
	var exchanges []surface.Exchange
	for _, turn := range turns {
		if turn.User == "" && turn.Assistant == "" {
			continue
		}
		exchanges = append(exchanges, surface.Exchange{User: turn.User, Assistant: turn.Assistant})
	}

	if len(exchanges) > n {
		exchanges = exchanges[len(exchanges)-n:]
	}
	return exchanges, nil
}
