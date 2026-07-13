package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

type Codex struct {
	rendererURL string
	nodeURL     string
}

func NewCodex(remoteURL string) *Codex {
	if remoteURL == "" {
		remoteURL = "9231"
	}
	if !strings.Contains(remoteURL, "://") {
		remoteURL = "ws://127.0.0.1:" + remoteURL
	}
	return &Codex{rendererURL: remoteURL, nodeURL: "ws://127.0.0.1:9229"}
}

func (c *Codex) Name() surface.SurfaceKind { return surface.KindCodex }

func (c *Codex) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: true, Reply: true, Goal: true,
		Compact: true, Model: true, Interrupt: true, Steer: true, Fork: false,
	}
}

type cdpConn struct {
	ws         *websocket.Conn
	mu         sync.Mutex
	next       int
	trampoline bool
}

func (c *Codex) dial(ctx context.Context) (*cdpConn, error) {
	targets, err := c.resolveCDPTargets(ctx)
	if err != nil {
		return nil, err
	}
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	var failures []string
	for _, target := range targets {
		ws, _, dialErr := d.DialContext(ctx, target.wsURL, http.Header{})
		if dialErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", target.wsURL, dialErr))
			continue
		}
		conn := &cdpConn{ws: ws, next: 1, trampoline: target.trampoline}
		value, probeErr := conn.evaluate(ctx, codexRendererCapabilityJS, 2*time.Second)
		if probeErr == nil && value == true {
			return conn, nil
		}
		_ = conn.close()
		failures = append(failures, fmt.Sprintf("%s: renderer bridge unavailable (%v)", target.wsURL, probeErr))
	}
	return nil, fmt.Errorf("connect Codex Desktop renderer: no debug target exposed electronBridge.sendMessageFromView (%s)", strings.Join(failures, "; "))
}

const codexRendererCapabilityJS = `typeof window?.electronBridge?.sendMessageFromView === 'function'`

type codexCDPTarget struct {
	wsURL      string
	trampoline bool
}

func (c *Codex) resolveCDPTargets(ctx context.Context) ([]codexCDPTarget, error) {
	targets, rendererErr := resolveCodexEndpoint(ctx, c.rendererURL, true)
	if rendererErr == nil {
		return targets, nil
	}
	if c.nodeURL != "" && c.nodeURL != c.rendererURL {
		if targets, nodeErr := resolveCodexEndpoint(ctx, c.nodeURL, false); nodeErr == nil {
			return targets, nil
		}
	}
	return nil, fmt.Errorf("connect Codex Desktop renderer at %s: %w (run 'agenthail launch codex'; an already-running app may need the compatibility bridge or a relaunch)", c.rendererURL, rendererErr)
}

func resolveCodexEndpoint(ctx context.Context, endpoint string, preferRenderer bool) ([]codexCDPTarget, error) {
	httpURL := strings.Replace(endpoint, "ws://", "http://", 1)
	httpURL = strings.Replace(httpURL, "wss://", "https://", 1)
	req, _ := http.NewRequestWithContext(ctx, "GET", httpURL+"/json", nil)
	resp, err := localHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("debug endpoint returned HTTP %d", resp.StatusCode)
	}
	var targets []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, fmt.Errorf("parse debug targets: %w", err)
	}
	var rendererTargets []codexCDPTarget
	var nodeTargets []codexCDPTarget
	for _, t := range targets {
		u, _ := t["webSocketDebuggerUrl"].(string)
		if u == "" {
			continue
		}
		targetType, _ := t["type"].(string)
		targetURL, _ := t["url"].(string)
		if preferRenderer && (targetType == "page" || targetType == "window") && strings.HasPrefix(targetURL, "app://") {
			target := codexCDPTarget{wsURL: u}
			if targetURL == "app://-/index.html" {
				rendererTargets = append([]codexCDPTarget{target}, rendererTargets...)
			} else {
				rendererTargets = append(rendererTargets, target)
			}
		}
		if targetType == "node" {
			nodeTargets = append(nodeTargets, codexCDPTarget{wsURL: u, trampoline: true})
		}
	}
	if len(rendererTargets) > 0 {
		return rendererTargets, nil
	}
	if len(nodeTargets) > 0 {
		return nodeTargets, nil
	}
	return nil, fmt.Errorf("no Codex renderer%s target found", map[bool]string{true: " or compatibility node", false: ""}[preferRenderer])
}

func (c *cdpConn) close() error { return c.ws.Close() }

func (c *cdpConn) evaluate(ctx context.Context, expr string, timeout time.Duration) (any, error) {
	if c.trampoline {
		expr = codexRendererTrampolineJS(expr)
	}
	c.mu.Lock()
	id := c.next
	c.next++
	c.mu.Unlock()

	req := map[string]any{
		"id":     id,
		"method": "Runtime.evaluate",
		"params": map[string]any{
			"expression":    expr,
			"awaitPromise":  true,
			"returnByValue": true,
		},
	}
	c.mu.Lock()
	err := c.ws.WriteJSON(req)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.ws.SetReadDeadline(time.Now().Add(timeout))
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		var msg map[string]any
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		msgID, _ := msg["id"].(float64)
		if int(msgID) != id {
			continue
		}
		result, _ := msg["result"].(map[string]any)
		if details, _ := result["exceptionDetails"].(map[string]any); details != nil {
			text, _ := details["text"].(string)
			if exception, _ := details["exception"].(map[string]any); exception != nil {
				if description, _ := exception["description"].(string); description != "" {
					text = description
				}
			}
			return nil, fmt.Errorf("eval error: %s", text)
		}
		res, _ := result["result"].(map[string]any)
		if sub, _ := res["subtype"].(string); sub == "error" {
			desc, _ := res["description"].(string)
			return nil, fmt.Errorf("eval error: %s", desc)
		}
		return res["value"], nil
	}
	return nil, fmt.Errorf("timeout waiting for eval response")
}

const maxCodexListPages = 100

func (c *Codex) List(ctx context.Context) ([]surface.Session, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return nil, err
	}
	var out []surface.Session
	var cursor any
	seenCursors := map[string]bool{}
	for page := 0; page < maxCodexListPages; page++ {
		sessions, nextCursor, err := c.listPage(ctx, conn, cursor)
		if err != nil {
			return nil, fmt.Errorf("thread/list: %w", err)
		}
		out = append(out, sessions...)
		cursor = nextCursor
		if cursor == nil {
			break
		}
		cursorKey := fmt.Sprint(cursor)
		if seenCursors[cursorKey] {
			return nil, fmt.Errorf("thread/list returned repeated cursor %q", cursorKey)
		}
		seenCursors[cursorKey] = true
	}
	if cursor != nil {
		return nil, fmt.Errorf("thread/list exceeded pagination limit of %d pages", maxCodexListPages)
	}
	return out, nil
}

func (c *Codex) listPage(ctx context.Context, conn *cdpConn, cursor any) ([]surface.Session, any, error) {
	params := map[string]any{}
	if cursor != nil {
		params["cursor"] = cursor
	}
	resp, err := c.rpc(ctx, conn, "thread/list", params, 10*time.Second)
	if err != nil {
		return nil, nil, err
	}
	result, _ := resp["result"].(map[string]any)
	threads, _ := result["data"].([]any)
	sessions := make([]surface.Session, 0, len(threads))
	for _, value := range threads {
		thread, _ := value.(map[string]any)
		session := surface.Session{
			ID:      str(thread, "id"),
			Surface: surface.KindCodex,
			Name:    surface.DeriveName(str(thread, "name"), str(thread, "preview"), 60),
			Cwd:     str(thread, "cwd"),
			Status:  codexStatus(thread["status"]),
		}
		if timestamp, ok := thread["recencyAt"].(float64); ok && timestamp > 0 {
			session.LastActive = time.Unix(int64(timestamp), 0)
		}
		sessions = append(sessions, session)
	}
	return sessions, result["nextCursor"], nil
}

func codexStatus(s any) surface.SessionStatus {
	if m, ok := s.(map[string]any); ok {
		s = m["type"]
	}
	str, _ := s.(string)
	switch strings.ToLower(str) {
	case "idle":
		return surface.StatusIdle
	case "busy", "running", "in_progress", "inprogress", "active":
		return surface.StatusBusy
	case "":
		return surface.StatusUnknown
	default:
		return surface.SessionStatus(str)
	}
}

func (c *Codex) Resolve(ctx context.Context, target string) (*surface.Session, error) {
	if looksLikeUUID(target) {
		conn, err := c.dial(ctx)
		if err != nil {
			return nil, err
		}
		defer conn.close()
		if err := c.ensureHooked(ctx, conn); err != nil {
			return nil, err
		}
		thread, err := c.readThread(ctx, conn, target)
		if err != nil {
			return nil, err
		}
		return &surface.Session{ID: thread.ID, Surface: surface.KindCodex, Name: thread.Name, Cwd: thread.Cwd, Status: codexObservation(thread).Status}, nil
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return nil, err
	}
	lower := strings.ToLower(target)
	var matches []surface.Session
	var exactMatches []surface.Session
	var cursor any
	seenCursors := map[string]bool{}
	for page := 0; page < maxCodexListPages; page++ {
		sessions, nextCursor, listErr := c.listPage(ctx, conn, cursor)
		if listErr != nil {
			return nil, fmt.Errorf("thread/list: %w", listErr)
		}
		for _, session := range sessions {
			if strings.EqualFold(session.Name, target) {
				exactMatches = append(exactMatches, session)
				continue
			}
			if strings.HasPrefix(session.ID, target) ||
				strings.Contains(strings.ToLower(session.Cwd), lower) ||
				strings.Contains(strings.ToLower(session.Name), lower) {
				matches = append(matches, session)
			}
		}
		cursor = nextCursor
		if cursor == nil {
			break
		}
		cursorKey := fmt.Sprint(cursor)
		if seenCursors[cursorKey] {
			return nil, fmt.Errorf("thread/list returned repeated cursor %q", cursorKey)
		}
		seenCursors[cursorKey] = true
	}
	if cursor != nil {
		return nil, fmt.Errorf("thread/list exceeded pagination limit of %d pages", maxCodexListPages)
	}
	if len(exactMatches) > 0 {
		matches = exactMatches
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no codex session matched '%s'", target)
	}
	if len(matches) > 1 {
		var lines []string
		for _, m := range matches {
			lines = append(lines, fmt.Sprintf("  %s cwd=%s", m.ID, m.Cwd))
		}
		return nil, fmt.Errorf("ambiguous target '%s':\n%s", target, strings.Join(lines, "\n"))
	}
	return &matches[0], nil
}

func (c *Codex) Observe(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return nil, err
	}
	thread, err := c.readThread(ctx, conn, sess.ID)
	if err != nil {
		return nil, err
	}
	return codexObservation(thread), nil
}

func (c *Codex) activeTurnID(ctx context.Context, conn *cdpConn, threadID string) (string, error) {
	thread, err := c.readThread(ctx, conn, threadID)
	if err != nil {
		return "", err
	}
	for i := len(thread.Turns) - 1; i >= 0; i-- {
		if thread.Turns[i].Status == surface.StatusBusy {
			return thread.Turns[i].ID, nil
		}
	}
	return "", nil
}

func (c *Codex) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	return c.SendWithOptions(ctx, sess, message, surface.SendOptions{})
}

func (c *Codex) SendWithOptions(ctx context.Context, sess *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return nil, err
	}
	if _, err := c.rpc(ctx, conn, "thread/resume", map[string]any{"threadId": sess.ID}, 5*time.Second); err != nil {
		return nil, fmt.Errorf("thread/resume: %w", err)
	}
	active, err := c.activeTurnID(ctx, conn, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("inspect active turn: %w", err)
	}
	if active != "" {
		return &surface.SendResult{UUID: sess.ID, Accepted: false}, nil
	}
	params := map[string]any{
		"threadId": sess.ID,
		"input":    []map[string]any{{"type": "text", "text": message}},
	}
	if options.Model != "" {
		params["model"] = options.Model
	}
	resp, err := c.rpc(ctx, conn, "turn/start", params, 10*time.Second)
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("turn/start: %w", err))
	}
	turnID := sess.ID
	if result, _ := resp["result"].(map[string]any); result != nil {
		if turn, _ := result["turn"].(map[string]any); turn != nil && str(turn, "id") != "" {
			turnID = str(turn, "id")
		} else if value := str(result, "turnId"); value != "" {
			turnID = value
		}
	}
	return &surface.SendResult{UUID: turnID, Accepted: true}, nil
}

func (c *Codex) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	observation, err := c.Observe(ctx, sess)
	if err != nil {
		return nil, err
	}
	if observation.Reply == nil {
		return &surface.ReplyResult{Done: false}, nil
	}
	return observation.Reply, nil
}

func (c *Codex) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	if uuid != "" {
		thread, readErr := c.readThread(ctx, conn, sess.ID)
		if readErr != nil {
			return readErr
		}
		if turn := codexTurnByID(thread, uuid); turn != nil && turn.Done {
			if turn.Error != "" {
				return fmt.Errorf("Codex turn %s did not complete successfully: %s", uuid, turn.Error)
			}
			if turn.Assistant != "" {
				onEvent(surface.StreamEvent{Kind: "text", Text: turn.Assistant})
			}
			onEvent(surface.StreamEvent{Kind: "done"})
			return nil
		}
	}
	cursorValue, err := conn.evaluate(ctx, codexEventCursorJS, 2*time.Second)
	if err != nil {
		return err
	}
	cursor, _ := cursorValue.(float64)
	if uuid != "" {
		cursor = 0
	}
	emittedText := ""
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		v, err := conn.evaluate(ctx, codexEventsJS(int64(cursor)), 2*time.Second)
		if err != nil {
			return err
		}
		raw, _ := v.(string)
		var batch codexEventBatch
		if err := json.Unmarshal([]byte(raw), &batch); err != nil {
			return fmt.Errorf("parse Codex events: %w", err)
		}
		cursor = float64(batch.Cursor)
		for _, event := range batch.Events {
			if !codexContainsID(event.Params, sess.ID) || (uuid != "" && !codexContainsID(event.Params, uuid)) {
				continue
			}
			method := event.Method
			switch {
			case strings.Contains(strings.ToLower(method), "agentmessage"):
				if txt := codexEventText(event.Params); txt != "" {
					emittedText += txt
					onEvent(surface.StreamEvent{Kind: "text", Text: txt})
				}
			case strings.Contains(strings.ToLower(method), "tool"):
				if name := codexEventTool(event.Params); name != "" {
					onEvent(surface.StreamEvent{Kind: "tool_use", Text: name})
				}
			case codexCompletionMethod(method):
				thread, readErr := c.readThread(ctx, conn, sess.ID)
				if readErr != nil {
					return readErr
				}
				turn := codexTurnByID(thread, uuid)
				if turn != nil && turn.Error != "" {
					return fmt.Errorf("Codex turn %s did not complete successfully: %s", uuid, turn.Error)
				}
				if turn != nil && turn.Assistant != "" && turn.Assistant != emittedText {
					if emittedText == "" {
						onEvent(surface.StreamEvent{Kind: "text", Text: turn.Assistant})
					} else if strings.HasPrefix(turn.Assistant, emittedText) {
						onEvent(surface.StreamEvent{Kind: "text", Text: strings.TrimPrefix(turn.Assistant, emittedText)})
					} else {
						return fmt.Errorf("Codex stream history exceeded the retained event buffer; use 'agenthail reply %s' for the complete response", sess.ID)
					}
				}
				onEvent(surface.StreamEvent{Kind: "done"})
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("stream timed out after %s", timeout)
}

func codexTurnByID(thread *codexThread, turnID string) *codexTurn {
	if thread == nil || turnID == "" {
		return nil
	}
	for index := range thread.Turns {
		if thread.Turns[index].ID == turnID {
			return &thread.Turns[index]
		}
	}
	return nil
}

func (c *Codex) GoalSet(ctx context.Context, sess *surface.Session, text string) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	_, err = c.rpc(ctx, conn, "thread/goal/set", map[string]any{
		"threadId":  sess.ID,
		"objective": text,
		"status":    "active",
	}, 5*time.Second)
	return err
}

func (c *Codex) GoalClear(ctx context.Context, sess *surface.Session) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	_, err = c.rpc(ctx, conn, "thread/goal/clear", map[string]any{
		"threadId": sess.ID,
	}, 5*time.Second)
	return err
}

func (c *Codex) GoalGet(ctx context.Context, sess *surface.Session) (*surface.GoalState, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return nil, err
	}
	resp, err := c.rpc(ctx, conn, "thread/goal/get", map[string]any{
		"threadId": sess.ID,
	}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	result, _ := resp["result"].(map[string]any)
	goal, _ := result["goal"].(map[string]any)
	if goal == nil {
		return nil, nil
	}
	return &surface.GoalState{
		Objective: str(goal, "objective"),
		Status:    str(goal, "status"),
	}, nil
}

func (c *Codex) Compact(ctx context.Context, sess *surface.Session) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	_, err = c.rpc(ctx, conn, "thread/compact/start", map[string]any{
		"threadId": sess.ID,
	}, 10*time.Second)
	return err
}

func (c *Codex) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return "", err
	}
	params := map[string]any{"threadId": sess.ID}
	if name != "" {
		params["model"] = name
	}
	response, err := c.rpc(ctx, conn, "thread/resume", params, 5*time.Second)
	if err != nil {
		return "", err
	}
	result, _ := response["result"].(map[string]any)
	model := str(result, "model")
	if model == "" {
		return "", fmt.Errorf("thread/resume response did not include the active model")
	}
	return model, nil
}

func (c *Codex) Models(ctx context.Context) ([]surface.ModelOption, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return nil, err
	}
	var models []surface.ModelOption
	var cursor any
	for page := 0; page < 20; page++ {
		params := map[string]any{"includeHidden": false, "limit": 100}
		if cursor != nil {
			params["cursor"] = cursor
		}
		response, err := c.rpc(ctx, conn, "model/list", params, 10*time.Second)
		if err != nil {
			return nil, err
		}
		result, _ := response["result"].(map[string]any)
		data, _ := result["data"].([]any)
		for _, raw := range data {
			value, _ := raw.(map[string]any)
			id := str(value, "model")
			if id == "" {
				id = str(value, "id")
			}
			if id == "" {
				continue
			}
			models = append(models, surface.ModelOption{ID: id, DisplayName: str(value, "displayName"), Description: str(value, "description"), Default: value["isDefault"] == true})
		}
		cursor = result["nextCursor"]
		if cursor == nil {
			break
		}
	}
	return models, nil
}

func (c *Codex) Interrupt(ctx context.Context, sess *surface.Session) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	turnID, err := c.activeTurnID(ctx, conn, sess.ID)
	if err != nil {
		return err
	}
	if turnID == "" {
		return fmt.Errorf("session idle; nothing to interrupt")
	}
	_, err = c.rpc(ctx, conn, "turn/interrupt", map[string]any{
		"threadId": sess.ID,
	}, 5*time.Second)
	return err
}

func (c *Codex) Steer(ctx context.Context, sess *surface.Session, message string) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	turnID, err := c.activeTurnID(ctx, conn, sess.ID)
	if err != nil {
		return err
	}
	if turnID == "" {
		return fmt.Errorf("session idle; nothing to steer (use 'send' instead)")
	}
	_, err = c.rpc(ctx, conn, "turn/steer", map[string]any{
		"threadId":       sess.ID,
		"expectedTurnId": turnID,
		"input":          []map[string]any{{"type": "text", "text": message}},
	}, 5*time.Second)
	return err
}

var localHTTPClient = &http.Client{Timeout: 5 * time.Second}

func (c *Codex) Tail(ctx context.Context, sess *surface.Session, n int) ([]surface.Exchange, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return nil, err
	}
	thread, err := c.readThread(ctx, conn, sess.ID)
	if err != nil {
		return nil, err
	}

	var exchanges []surface.Exchange
	for _, turn := range thread.Turns {
		if turn.User != "" || turn.Assistant != "" {
			exchanges = append(exchanges, surface.Exchange{User: turn.User, Assistant: turn.Assistant})
		}
	}

	if len(exchanges) > n {
		exchanges = exchanges[len(exchanges)-n:]
	}
	return exchanges, nil
}
