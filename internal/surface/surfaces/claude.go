package surfaces

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	observeMu    sync.Mutex
	observeState map[string]*claudeObservationState
	modelsMu     sync.Mutex
	modelsCache  []surface.ModelOption
	modelsAt     time.Time
	modelsFlight *claudeModelsFlight
	subagents    *claudeSubagentObserver
	request      ClaudeRequest
}

type ClaudeRequest func(context.Context, string, string, map[string]string, string, string, string, time.Duration) (int, string, error)

func NewClaude(profile, home string) *Claude {
	return NewClaudeWithRequest(profile, home, claudeSendRequest)
}

func NewClaudeWithRequest(profile, home string, request ClaudeRequest) *Claude {
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
	return &Claude{profile: profile, home: home, cookieBridge: bridge, subagents: newClaudeSubagentObserver(), request: request}
}

func (c *Claude) Name() surface.SurfaceKind { return surface.KindClaude }

func (c *Claude) ObserveClaudeRuns(ctx context.Context) ([]surface.ClaudeRunObservation, error) {
	return ObserveClaudeRuns(ctx, c.home)
}

func (c *Claude) ObserveClaudeSubagents(ctx context.Context, session *surface.Session) ([]surface.ClaudeSubagentLink, error) {
	return c.subagents.observeSession(ctx, session)
}

func (c *Claude) ObserveAllClaudeSubagents(ctx context.Context) ([]surface.ClaudeSubagentLink, error) {
	return ObserveAllClaudeSubagents(ctx, c.home)
}

func (c *Claude) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: true, Reply: true, Goal: false,
		Compact: true, Model: true, Interrupt: true, Steer: true,
	}
}

func (c *Claude) Health(ctx context.Context) error {
	if sessions, err := c.List(ctx); err == nil {
		for _, session := range sessions {
			if session.Transport == "uds" {
				return nil
			}
		}
	}
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

var claudeProjectDirPattern = regexp.MustCompile(`[^A-Za-z0-9]`)

func (c *Claude) transcriptPath(s *surface.Session) string {
	return c.resolveTranscript(s, s.ID)
}

// resolveTranscript finds a conversation's transcript. Claude Code names the
// project directory after the launch directory with every character outside
// [A-Za-z0-9] replaced by '-'. A conversation that changed directory keeps
// its launch project, so an unmatched id is looked up across projects.
func (c *Claude) resolveTranscript(s *surface.Session, conversationID string) string {
	if conversationID == "" || strings.ContainsAny(conversationID, `/\`) {
		return ""
	}
	projects := filepath.Join(c.home, ".claude", "projects")
	if s.Cwd != "" {
		path := filepath.Join(projects, claudeProjectDir(s.Cwd), conversationID+".jsonl")
		if fileExists(path) {
			return path
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(projects, "*", conversationID+".jsonl")); len(matches) == 1 {
		return matches[0]
	}
	if s.Cwd == "" {
		return ""
	}
	return filepath.Join(projects, claudeProjectDir(s.Cwd), conversationID+".jsonl")
}

func claudeProjectDir(cwd string) string {
	return claudeProjectDirPattern.ReplaceAllString(cwd, "-")
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
		if str(m, "agenthail") == "peer-worker" {
			continue
		}
		socket := c.peerSocket(ctx, m)
		if bridge == "" && socket != "" {
			bridge = str(m, "sessionId")
		}
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
		if socket != "" {
			sess.Transport = "uds"
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
		sess.Status = claudePeerStatus(m)
		sess.Transcript = c.resolveTranscript(&sess, str(m, "sessionId"))
		sess.HasLocal = sess.Transcript != "" && fileExists(sess.Transcript)
		if sess.Name == "" {
			sess.Name = c.firstUserMessage(sess.Transcript)
		}
		if sess.HasLocal {
			if observation, observeErr := c.Observe(ctx, &sess); observeErr == nil && observation.Status != surface.StatusUnknown {
				sess.Status = observation.Status
			}
			if rollup, rollupErr := c.subagents.rollup(ctx, &sess); rollupErr == nil {
				sess.Subagents = rollup
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

func claudePeerStatus(record map[string]any) surface.SessionStatus {
	status := claudeStatus(surface.SessionStatus(str(record, "status")))
	if status != surface.StatusIdle {
		return status
	}
	features, _ := record["peerFeatures"].([]any)
	for _, feature := range features {
		if feature == "notify_idle" {
			return status
		}
	}
	return surface.StatusUnknown
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
			strings.TrimSuffix(filepath.Base(s.Transcript), ".jsonl") == target ||
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
	state, err := c.observeTranscript(ctx, path)
	if err != nil {
		return nil, err
	}
	compactPending := state.compactPending()
	if !state.hasCurrent {
		transcriptCanSetReady := observation.Status != surface.StatusOffline || claudeProcessAlive(sess.PID)
		if compactPending && transcriptCanSetReady {
			observation.Status = surface.StatusBusy
			observation.ActiveTurnID = "compact"
		} else if observation.Status != surface.StatusBusy && observation.Status != surface.StatusOffline {
			observation.Status = surface.StatusUnknown
		}
		return observation, nil
	}
	last := state.current
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
	if state.hasCompleted && state.completed.MessageID != "" {
		observation.CompletedTurnID = state.completed.MessageID
		observation.InputTurnID = state.completed.UserID
		observation.Reply = &surface.ReplyResult{Text: state.completed.Assistant, UserText: state.completed.User, Done: true}
	}
	return observation, nil
}

func (c *Claude) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	if sess.Transport == "uds" {
		if strings.HasPrefix(strings.TrimSpace(message), "/") {
			return nil, surface.DeliveryTerminal(fmt.Errorf("Claude peer messages cannot execute slash commands; use the Claude session directly"), surface.DeliveryInvalidRequest)
		}
		return c.sendPeer(ctx, sess, message)
	}
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
	request := c.request
	if request == nil {
		request = claudeSendRequest
	}
	status, respBody, err := request(ctx, "POST",
		"https://claude.ai/v1/code/sessions/"+cse+"/events",
		headers, string(bodyBytes), c.cookieBridge, "https://claude.ai/", 30*time.Second)
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	if strings.Contains(respBody, "Just a moment") {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("cloudflare challenge (cf_clearance may need refresh)"))
	}
	switch status {
	case http.StatusBadRequest:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryInvalidRequest)
	case http.StatusUnauthorized:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryAuthenticationNeeded)
	case http.StatusForbidden:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryAccessDenied)
	case http.StatusNotFound:
		return nil, surface.DeliveryTerminal(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)), surface.DeliveryTargetMissing)
	}
	if status != http.StatusOK {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("send failed (HTTP %d): %s", status, diagnosticExcerpt(respBody)))
	}
	return &surface.SendResult{UUID: uuid, Accepted: true}, nil
}

func (c *Claude) sendCommand(ctx context.Context, sess *surface.Session, cmd string) error {
	_, err := c.postMessage(ctx, sess, cmd)
	return err
}

func (c *Claude) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	read, err := c.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: max(1, limit)})
	if err != nil {
		return nil, err
	}
	if read.Reply == nil {
		return &surface.ReplyResult{Done: false, Source: read.Source}, nil
	}
	return read.Reply, nil
}

func (c *Claude) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	if sess.Transport == "uds" && uuid != "" {
		return surface.ErrUnsupported
	}
	return c.streamTimeline(ctx, sess, uuid, onEvent, timeout)
}

func (c *Claude) streamTimeline(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	if path == "" || !fileExists(path) {
		return fmt.Errorf("no local transcript for streaming")
	}
	state, err := c.observeTranscript(ctx, path)
	if err != nil {
		return err
	}
	identity := transcriptFileIdentity(state.fileInfo)
	offset := state.offset
	if uuid != "" {
		// A targeted wait looks back so a turn submitted before the tail started is still found;
		// events outside that turn are filtered below.
		offset = max(int64(0), state.offset-initialClaudeObservationBytes)
	} else if sess.TranscriptOffsetSet && sess.TranscriptOffset <= state.offset && identity != "" && sess.TranscriptIdentity == identity {
		// The session source resumes at its seed or previous window boundary, so records written
		// in between are delivered once without replaying history the seed bounded away.
		offset = sess.TranscriptOffset
	}
	if uuid != "" && offset > 0 {
		file, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		if _, seekErr := file.Seek(offset, 0); seekErr != nil {
			file.Close()
			return seekErr
		}
		reader := bufio.NewReader(file)
		prefix, readErr := reader.ReadBytes('\n')
		file.Close()
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		offset += int64(len(prefix))
	}
	deadline := time.Now().Add(timeout)
	currentTurnID := ""
	if state.hasCurrent {
		hasTurnStart, scanErr := claudeTranscriptHasTurnStart(ctx, path, offset)
		if scanErr != nil {
			return scanErr
		}
		if !hasTurnStart {
			currentTurnID = state.current.UserID
		}
	}
	terminalTurns := map[string]bool{}
	for time.Now().Before(deadline) {
		lineOffset := offset
		completed := false
		next, scanErr := scanAppendedJSONL(ctx, path, offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
			recordOffset := lineOffset
			lineOffset += int64(len(line))
			var record map[string]any
			if json.Unmarshal(line, &record) != nil {
				return nil
			}
			turnID := currentTurnID
			if claudeRecordStartsTurn(record) {
				currentTurnID = str(record, "uuid")
				turnID = currentTurnID
			}
			done, interrupted := claudeStreamTerminal(record)
			if interrupted {
				if uuid != "" && turnID == uuid {
					return fmt.Errorf("Claude turn %s was interrupted", uuid)
				}
				if turnID != "" && !terminalTurns[turnID] {
					terminalTurns[turnID] = true
					key := stableTimelineItemID(recordOffset, line, 0)
					onEvent(surface.StreamEvent{ID: key, ProviderKey: "timeline:" + key, Version: 1, Operation: "phase", TurnID: turnID, Kind: "done", Status: "cancelled"})
				}
				return nil
			}
			items := claudeTimelineItems(record)
			if err := decorateTimelineAttachments(ctx, items, record, "claude", recordOffset); err != nil {
				return err
			}
			if uuid != "" && turnID != uuid {
				return nil
			}
			for index, item := range items {
				key := stableTimelineItemID(recordOffset, line, index)
				version := uint64(len(item.Text))
				if version == 0 {
					version = 1
				}
				at, _ := time.Parse(time.RFC3339Nano, str(record, "timestamp"))
				onEvent(surface.StreamEvent{ID: key, ProviderKey: "timeline:" + key, Version: version, Operation: "upsert", Final: true, TurnID: turnID, Role: item.Role, Title: item.Title, CallID: item.CallID, Status: item.Status, Attachment: item.Attachment, Truncated: item.Truncated, TruncationReason: item.TruncationReason, Timestamp: at, Kind: item.Kind, Text: item.Text})
			}
			if done && turnID != "" && !terminalTurns[turnID] {
				terminalTurns[turnID] = true
				key := stableTimelineItemID(recordOffset, line, len(items))
				onEvent(surface.StreamEvent{ID: key, ProviderKey: "timeline:" + key, Version: 1, Operation: "phase", TurnID: turnID, Kind: "done"})
				if uuid != "" && turnID == uuid {
					completed = true
				}
			}
			return nil
		})
		if scanErr != nil {
			return scanErr
		}
		offset = next
		if uuid == "" {
			sess.TranscriptOffset = offset
			sess.TranscriptOffsetSet = true
			sess.TranscriptIdentity = identity
		}
		if completed {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(transcriptPollInterval):
		}
	}
	return fmt.Errorf("stream timed out after %s: %w", timeout, surface.ErrStreamWindow)
}

var errClaudeTurnStartFound = errors.New("claude turn start found")

func claudeTranscriptHasTurnStart(ctx context.Context, path string, offset int64) (bool, error) {
	_, err := scanAppendedJSONL(ctx, path, offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
		if !bytes.Contains(line, []byte(`"user"`)) {
			return nil
		}
		var record map[string]any
		if json.Unmarshal(line, &record) == nil && claudeRecordStartsTurn(record) {
			return errClaudeTurnStartFound
		}
		return nil
	})
	if errors.Is(err, errClaudeTurnStartFound) {
		return true, nil
	}
	return false, err
}

func claudeRecordStartsTurn(record map[string]any) bool {
	return str(record, "type") == "user" && str(record, "uuid") != "" && !claudeRecordHasToolResult(record) && !claudeRecordIsInterrupt(record)
}

func claudeRecordHasToolResult(record map[string]any) bool {
	message, _ := record["message"].(map[string]any)
	blocks, _ := message["content"].([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if str(block, "type") == "tool_result" {
			return true
		}
	}
	return str(message, "tool_use_id") != ""
}

func claudeRecordIsInterrupt(record map[string]any) bool {
	if str(record, "type") != "user" {
		return false
	}
	message, _ := record["message"].(map[string]any)
	return isClaudeInterruptMarker(strings.TrimSpace(transcriptText(message["content"])))
}

func claudeStreamTerminal(record map[string]any) (done, interrupted bool) {
	switch str(record, "type") {
	case "system":
		return str(record, "subtype") == "turn_duration", false
	case "assistant":
		reason := strNested(record, "message", "stop_reason")
		return reason == "end_turn", claudeTerminalInterruption(reason)
	case "user":
		return false, claudeRecordIsInterrupt(record)
	default:
		return false, false
	}
}

func strNested(record map[string]any, object, field string) string {
	value, _ := record[object].(map[string]any)
	return str(value, field)
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
	if nativeClaudeOnly(sess) {
		return surface.ErrUnsupported
	}
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("compact confirmation requires a local transcript: %w", err)
	}
	result, err := c.postMessage(ctx, sess, "/compact")
	if err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return surface.DeliveryOutcomeUnknown(fmt.Errorf("compact was accepted but completion was not observed: %w", ctx.Err()))
		case <-ticker.C:
			completed, readErr := readClaudeCompactCompletion(path, info.Size(), result.UUID)
			if readErr != nil {
				return surface.DeliveryOutcomeUnknown(fmt.Errorf("read compact completion: %w", readErr))
			}
			if completed {
				return nil
			}
		}
	}
}

func (c *Claude) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	if nativeClaudeOnly(sess) && name != "" {
		return "", surface.ErrUnsupported
	}
	if name != "" {
		result, err := c.confirmedCommand(ctx, sess, "/model", name, 5*time.Second)
		if err != nil {
			return "", fmt.Errorf("model switch rejected: %w", err)
		}
		sess.ConfiguredModel = strings.TrimSpace(result)
		return result, nil
	}
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	turns, err := readClaudeTailTurns(ctx, path)
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
	if nativeClaudeOnly(sess) {
		return surface.ErrUnsupported
	}
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
	if nativeClaudeOnly(sess) {
		return surface.ErrUnsupported
	}
	current, err := c.Resolve(ctx, sess.ID)
	if err != nil {
		return err
	}
	if current.Status != surface.StatusBusy {
		return surface.DeliveryUnavailable(errors.New("session idle; nothing to steer (use 'send' instead)"))
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
	read, err := c.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: n})
	if err != nil {
		return nil, err
	}
	return read.Exchanges, nil
}
