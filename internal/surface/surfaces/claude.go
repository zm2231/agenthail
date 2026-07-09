package surfaces

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type Claude struct {
	profile      string
	home         string
	cookieBridge string
}

func NewClaude(profile, home string) *Claude {
	if profile == "" {
		profile = "Default"
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return &Claude{profile: profile, home: home, cookieBridge: cookieBridgePath("cookie")}
}

func (c *Claude) Name() surface.SurfaceKind { return surface.KindClaude }

func (c *Claude) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: true, Reply: true, Goal: true,
		Compact: true, Model: true, Interrupt: true, Steer: true,
	}
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
		return nil, nil
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
		if n, ok := m["name"].(string); ok {
			sess.Name = n
		}
		if ts, ok := m["updatedAt"].(float64); ok && ts > 0 {
			sess.LastActive = time.UnixMilli(int64(ts))
		}
		sess.Transcript = c.resolveTranscript(&sess, str(m, "sessionId"))
		sess.HasLocal = sess.Transcript != "" && fileExists(sess.Transcript)
		if sess.Name == "" {
			sess.Name = c.firstUserMessage(sess.Transcript)
		}
		out = append(out, sess)
	}
	return out, nil
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

func (c *Claude) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
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
	status, respBody, err := sidecarPostWithCookies(
		"https://claude.ai/v1/code/sessions/"+cse+"/events",
		headers, string(bodyBytes), c.cookieBridge, "https://claude.ai/", 30*time.Second)
	if err != nil {
		return nil, err
	}
	if strings.Contains(respBody, "Just a moment") {
		return nil, fmt.Errorf("cloudflare challenge (cf_clearance may need refresh)")
	}
	if status != 200 {
		return nil, fmt.Errorf("send failed (HTTP %d): %s", status, respBody)
	}
	return &surface.SendResult{UUID: uuid, Accepted: true}, nil
}

func (c *Claude) sendCommand(ctx context.Context, sess *surface.Session, cmd string) error {
	_, err := c.Send(ctx, sess, cmd)
	return err
}

func (c *Claude) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	if path == "" || !fileExists(path) {
		return &surface.ReplyResult{Error: "no local transcript"}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec["type"] != "assistant" {
			continue
		}
		msg, _ := rec["message"].(map[string]any)
		content, _ := msg["content"].([]any)
		for _, item := range content {
			if m, ok := item.(map[string]any); ok && m["type"] == "text" {
				if t, ok := m["text"].(string); ok && strings.TrimSpace(t) != "" {
					return &surface.ReplyResult{Text: t, Done: true}, nil
				}
			}
		}
	}
	return &surface.ReplyResult{Done: false}, nil
}

func (c *Claude) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	path := c.transcriptPath(sess)
	if path == "" || !fileExists(path) {
		return fmt.Errorf("no local transcript for streaming")
	}
	deadline := time.Now().Add(timeout)
	info, _ := os.Stat(path)
	pos := info.Size()
	started := false
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		info, err := os.Stat(path)
		if err != nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		size := info.Size()
		if size < pos {
			pos = 0
		}
		if size > pos {
			f, _ := os.Open(path)
			f.Seek(pos, 0)
			chunk := make([]byte, size-pos)
			f.Read(chunk)
			f.Close()
			pos = size
			for _, line := range strings.Split(string(chunk), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var rec map[string]any
				if json.Unmarshal([]byte(line), &rec) != nil {
					continue
				}
				if !started {
					if id, _ := rec["uuid"].(string); id == uuid && rec["type"] == "user" {
						started = true
						onEvent(surface.StreamEvent{Kind: "status", Text: "turn queued"})
					}
					continue
				}
				t, _ := rec["type"].(string)
				switch t {
				case "assistant":
					msg, _ := rec["message"].(map[string]any)
					content, _ := msg["content"].([]any)
					for _, item := range content {
						m, _ := item.(map[string]any)
						switch m["type"] {
						case "text":
							onEvent(surface.StreamEvent{Kind: "text", Text: m["text"].(string)})
						case "tool_use":
							onEvent(surface.StreamEvent{Kind: "tool_use", Text: m["name"].(string)})
						}
					}
					if msg["stop_reason"] == "end_turn" {
						onEvent(surface.StreamEvent{Kind: "done"})
						return nil
					}
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("stream timed out after %s", timeout)
}

func (c *Claude) GoalSet(ctx context.Context, sess *surface.Session, text string) error {
	return c.sendCommand(ctx, sess, "/goal "+text)
}

func (c *Claude) GoalClear(ctx context.Context, sess *surface.Session) error {
	return c.sendCommand(ctx, sess, "/goal clear")
}

func (c *Claude) GoalGet(ctx context.Context, sess *surface.Session) (*surface.GoalState, error) {
	return nil, nil
}

func (c *Claude) Compact(ctx context.Context, sess *surface.Session) error {
	return c.sendCommand(ctx, sess, "/compact")
}

func (c *Claude) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	if name != "" {
		return "", c.sendCommand(ctx, sess, "/model "+name)
	}
	return "", nil
}

func (c *Claude) Interrupt(ctx context.Context, sess *surface.Session) error {
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
	status, respBody, err := sidecarPostWithCookies(
		"https://claude.ai/v1/code/sessions/"+cse+"/events",
		headers, string(bodyBytes), c.cookieBridge, "https://claude.ai/", 10*time.Second)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("interrupt failed (HTTP %d): %s", status, respBody)
	}
	return nil
}

func (c *Claude) Steer(ctx context.Context, sess *surface.Session, message string) error {
	_, err := c.Send(ctx, sess, message)
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
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	type msg struct {
		role string
		text string
	}
	var msgs []msg
	for sc.Scan() {
		var rec map[string]any
		if json.Unmarshal(sc.Bytes(), &rec) != nil {
			continue
		}
		typ, _ := rec["type"].(string)
		if typ != "user" && typ != "assistant" {
			continue
		}
		msgData, _ := rec["message"].(map[string]any)
		if typ == "assistant" {
			if msgData["stop_reason"] != "end_turn" {
				continue
			}
		}
		content := msgData["content"]
		text := ""
		switch c := content.(type) {
		case string:
			text = c
		case []any:
			for _, item := range c {
				if m, ok := item.(map[string]any); ok && m["type"] == "text" {
					if t, ok := m["text"].(string); ok && strings.TrimSpace(t) != "" {
						text = t
						break
					}
				}
			}
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if typ == "user" {
			if strings.HasPrefix(text, "<local-command") || strings.HasPrefix(text, "<command-") || strings.HasPrefix(text, "<system") {
				continue
			}
			if strings.HasPrefix(text, "[Request interrupted") || strings.HasPrefix(text, "[Request to") {
				continue
			}
			if _, hasToolID := msgData["tool_use_id"]; hasToolID {
				continue
			}
			if cl, ok := content.([]any); ok {
				for _, item := range cl {
					if m, ok := item.(map[string]any); ok && m["type"] == "tool_result" {
						text = ""
						break
					}
				}
			}
			if text == "" {
				continue
			}
		}
		msgs = append(msgs, msg{role: typ, text: text})
	}

	var exchanges []surface.Exchange
	for i := 0; i < len(msgs); i++ {
		if msgs[i].role == "user" {
			ex := surface.Exchange{User: msgs[i].text}
			if i+1 < len(msgs) && msgs[i+1].role == "assistant" {
				ex.Assistant = msgs[i+1].text
				i++
			}
			exchanges = append(exchanges, ex)
		} else if msgs[i].role == "assistant" {
			if len(exchanges) == 0 || exchanges[len(exchanges)-1].Assistant != "" {
				exchanges = append(exchanges, surface.Exchange{Assistant: msgs[i].text})
			} else {
				exchanges[len(exchanges)-1].Assistant = msgs[i].text
			}
		}
	}

	if len(exchanges) > n {
		exchanges = exchanges[len(exchanges)-n:]
	}
	return exchanges, nil
}
