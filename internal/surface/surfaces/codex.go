package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

type Codex struct {
	rendererURL  string
	nodeURL      string
	managed      bool
	bridgeMu     sync.Mutex
	bridgeTarget string
	bridgeErr    error
	bridgeRetry  time.Time
	runtimeMu    sync.Mutex
	runtimeReady bool
	contextMu    sync.Mutex
	contextState map[string]*codexContextState
}

func NewCodex(remoteURL string) *Codex {
	managed := remoteURL == "" || !strings.Contains(remoteURL, "://")
	if remoteURL == "" {
		remoteURL = "9231"
	}
	if !strings.Contains(remoteURL, "://") {
		remoteURL = "ws://127.0.0.1:" + remoteURL
	}
	return &Codex{rendererURL: remoteURL, nodeURL: "ws://127.0.0.1:9229", managed: managed}
}

func (c *Codex) Name() surface.SurfaceKind { return surface.KindCodex }

func (c *Codex) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: true, Reply: true, Goal: true,
		Compact: true, Model: true, Interrupt: true, Steer: true,
	}
}

func (c *Codex) Health(ctx context.Context) error {
	return codexHealth(ctx, c.managed, c.openDesktop, func(ctx context.Context) (codexClient, error) {
		return dialManagedCodex(ctx)
	})
}

type codexOpener func(context.Context) (codexClient, error)

func codexHealth(ctx context.Context, managed bool, desktop, managedRuntime codexOpener) error {
	client, desktopErr := desktop(ctx)
	if desktopErr == nil {
		return client.Close()
	}
	if managed {
		client, managedErr := managedRuntime(ctx)
		if managedErr == nil {
			return client.Close()
		}
		return fmt.Errorf("Codex Desktop bridge is unavailable: %v; managed Codex app-server is unavailable: %w", desktopErr, managedErr)
	}
	return fmt.Errorf("Codex Desktop bridge is unavailable: %w", desktopErr)
}

type cdpConn struct {
	ws         *websocket.Conn
	mu         sync.Mutex
	next       int
	trampoline bool
	target     string
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
		conn := &cdpConn{ws: ws, next: 1, trampoline: target.trampoline, target: target.wsURL}
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
	clients := []struct {
		client           codexClient
		managed          bool
		desktopReachable bool
	}{}
	var failures []string
	desktopReachable := false
	if client, err := c.openDesktop(ctx); err == nil {
		desktopReachable = true
		clients = append(clients, struct {
			client           codexClient
			managed          bool
			desktopReachable bool
		}{client, false, true})
	} else {
		failures = append(failures, err.Error())
	}
	if client, err := c.openManaged(ctx); err == nil && c.managed {
		clients = append(clients, struct {
			client           codexClient
			managed          bool
			desktopReachable bool
		}{client, true, desktopReachable})
	} else if c.managed {
		failures = append(failures, err.Error())
	}
	if len(clients) == 0 {
		return nil, fmt.Errorf("Codex is unavailable: %s", strings.Join(failures, "; "))
	}
	byID := map[string]surface.Session{}
	succeeded := false
	for _, entry := range clients {
		sessions, err := c.listClient(ctx, entry.client, entry.managed, entry.desktopReachable)
		_ = entry.client.Close()
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		succeeded = true
		for _, session := range sessions {
			previous, exists := byID[session.ID]
			if !exists || session.Transport == codexTransportManaged || previous.Transport == codexTransportReadOnly {
				byID[session.ID] = session
			}
		}
	}
	if !succeeded {
		return nil, fmt.Errorf("thread/list: %s", strings.Join(failures, "; "))
	}
	out := make([]surface.Session, 0, len(byID))
	for _, session := range byID {
		out = append(out, session)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out, nil
}

func (c *Codex) listClient(ctx context.Context, conn codexClient, managed, desktopReachable bool) ([]surface.Session, error) {
	var out []surface.Session
	var cursor any
	seenCursors := map[string]bool{}
	for page := 0; page < maxCodexListPages; page++ {
		sessions, nextCursor, err := c.listPage(ctx, conn, cursor, managed, desktopReachable)
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

func (c *Codex) listPage(ctx context.Context, conn codexClient, cursor any, managed, desktopReachable bool) ([]surface.Session, any, error) {
	params := map[string]any{}
	if cursor != nil {
		params["cursor"] = cursor
	}
	resp, err := conn.Request(ctx, "thread/list", params, 10*time.Second)
	if err != nil {
		return nil, nil, err
	}
	result, _ := resp["result"].(map[string]any)
	threads, _ := result["data"].([]any)
	sessions := make([]surface.Session, 0, len(threads))
	for _, value := range threads {
		thread, _ := value.(map[string]any)
		source := codexSource(thread["source"])
		if str(thread, "threadSource") == "agenthail" {
			source = "agenthail"
		}
		session := surface.Session{
			ID:        str(thread, "id"),
			Surface:   surface.KindCodex,
			Name:      surface.DeriveName(str(thread, "name"), str(thread, "preview"), 60),
			Cwd:       str(thread, "cwd"),
			Status:    codexStatus(thread["status"]),
			Source:    source,
			Transport: codexTransport(source, thread["status"], managed, desktopReachable),
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
	sessions, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(target)
	var matches []surface.Session
	var exactMatches []surface.Session
	for _, session := range sessions {
		if looksLikeUUID(target) && session.ID == target {
			copy := session
			return &copy, nil
		}
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
	conn, err := c.openSession(ctx, sess, false)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	thread, err := c.readThread(ctx, conn, sess.ID)
	if err != nil {
		return nil, err
	}
	return codexObservation(thread), nil
}

func (c *Codex) activeTurnID(ctx context.Context, conn codexClient, threadID string) (string, error) {
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

func (c *Codex) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	client, err := c.openManaged(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer client.Close()
	return c.startSession(ctx, client, options)
}

func (c *Codex) startSession(ctx context.Context, client codexClient, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	message := strings.TrimSpace(options.Message)
	if message == "" {
		return nil, nil, fmt.Errorf("message is required")
	}
	params := map[string]any{"threadSource": "agenthail", "serviceName": "agenthail"}
	if options.Cwd != "" {
		params["cwd"] = options.Cwd
	}
	if options.Model != "" {
		params["model"] = options.Model
	}
	if options.ApprovalPolicy != "" {
		params["approvalPolicy"] = options.ApprovalPolicy
	}
	response, err := client.Request(ctx, "thread/start", params, 10*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("thread/start: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	threadID := str(thread, "id")
	if threadID == "" {
		return nil, nil, fmt.Errorf("thread/start returned no thread id")
	}
	session := &surface.Session{
		ID:         threadID,
		Surface:    surface.KindCodex,
		Name:       surface.DeriveName(str(thread, "name"), message, 80),
		Cwd:        str(result, "cwd"),
		Status:     surface.StatusBusy,
		HasLocal:   true,
		Source:     "agenthail",
		Transport:  codexTransportManaged,
		LastActive: time.Now(),
	}
	if session.Cwd == "" {
		session.Cwd = options.Cwd
	}
	turnParams := map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": message}},
	}
	turnResponse, err := client.Request(ctx, "turn/start", turnParams, 10*time.Second)
	if err != nil {
		return session, nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("turn/start: %w", err))
	}
	turnID := threadID
	if turnResult, _ := turnResponse["result"].(map[string]any); turnResult != nil {
		if turn, _ := turnResult["turn"].(map[string]any); str(turn, "id") != "" {
			turnID = str(turn, "id")
		} else if value := str(turnResult, "turnId"); value != "" {
			turnID = value
		}
	}
	return session, &surface.SendResult{UUID: turnID, Accepted: true}, nil
}

func (c *Codex) SendWithOptions(ctx context.Context, sess *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	conn, err := c.openSession(ctx, sess, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.Request(ctx, "thread/resume", map[string]any{"threadId": sess.ID}, 5*time.Second); err != nil {
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
	resp, err := conn.Request(ctx, "turn/start", params, 10*time.Second)
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
	if sess.Transport == codexTransportManaged {
		return c.streamManaged(ctx, sess, uuid, onEvent, timeout)
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.close()
	if err := c.ensureHooked(ctx, conn); err != nil {
		return err
	}
	if uuid != "" {
		thread, readErr := c.readThread(ctx, &desktopCodexClient{owner: c, conn: conn}, sess.ID)
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
	var lastContext surface.ContextUsage
	var nextContextPoll time.Time
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
			if !codexContainsID(event.Params, sess.ID) {
				continue
			}
			if usage, ok := c.applyContextEvent(sess, event, lastContext); ok {
				lastContext = *usage
				onEvent(surface.StreamEvent{Kind: "context", Context: usage})
				continue
			}
			if uuid != "" && !codexContainsID(event.Params, uuid) {
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
				thread, readErr := c.readThread(ctx, &desktopCodexClient{owner: c, conn: conn}, sess.ID)
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
		if !time.Now().Before(nextContextPoll) {
			nextContextPoll = time.Now().Add(time.Second)
			if usage, usageErr := c.ContextUsage(ctx, sess); usageErr == nil && usage != nil && *usage != lastContext {
				lastContext = *usage
				onEvent(surface.StreamEvent{Kind: "context", Context: usage})
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("stream timed out after %s", timeout)
}

func (c *Codex) streamManaged(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	client, err := c.openSession(ctx, sess, false)
	if err != nil {
		return err
	}
	defer client.Close()
	return c.streamManagedClient(ctx, client, sess, uuid, onEvent, timeout)
}

func (c *Codex) streamManagedClient(ctx context.Context, client codexClient, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	emitted := ""
	baselineTurnID := ""
	var lastContext surface.ContextUsage
	var nextContextPoll time.Time
	if uuid == "" {
		thread, readErr := c.readThread(ctx, client, sess.ID)
		if readErr != nil {
			return readErr
		}
		if len(thread.Turns) > 0 {
			latest := thread.Turns[len(thread.Turns)-1]
			if latest.Done {
				baselineTurnID = latest.ID
			} else {
				uuid = latest.ID
			}
		}
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		thread, err := c.readThread(ctx, client, sess.ID)
		if err != nil {
			return err
		}
		if source, ok := client.(interface{ DrainNotifications() []codexEvent }); ok {
			for _, event := range source.DrainNotifications() {
				if !codexContainsID(event.Params, sess.ID) {
					continue
				}
				if usage, matched := c.applyContextEvent(sess, event, lastContext); matched {
					lastContext = *usage
					onEvent(surface.StreamEvent{Kind: "context", Context: usage})
				}
			}
		}
		if !time.Now().Before(nextContextPoll) {
			nextContextPoll = time.Now().Add(time.Second)
			if usage, usageErr := c.ContextUsage(ctx, sess); usageErr == nil && usage != nil && *usage != lastContext {
				lastContext = *usage
				onEvent(surface.StreamEvent{Kind: "context", Context: usage})
			}
		}
		turn := codexTurnByID(thread, uuid)
		if uuid == "" && len(thread.Turns) > 0 {
			latest := &thread.Turns[len(thread.Turns)-1]
			if latest.ID != baselineTurnID || !latest.Done {
				uuid = latest.ID
				turn = latest
			}
		}
		if turn != nil && strings.HasPrefix(turn.Assistant, emitted) {
			delta := strings.TrimPrefix(turn.Assistant, emitted)
			if delta != "" {
				emitted = turn.Assistant
				onEvent(surface.StreamEvent{Kind: "text", Text: delta})
			}
			if turn.Done {
				if turn.Error != "" {
					return fmt.Errorf("Codex turn %s did not complete successfully: %s", turn.ID, turn.Error)
				}
				onEvent(surface.StreamEvent{Kind: "done"})
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
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
	_, err := c.requestSession(ctx, sess, true, "thread/goal/set", map[string]any{
		"threadId":  sess.ID,
		"objective": text,
		"status":    "active",
	}, 5*time.Second)
	return err
}

func (c *Codex) GoalClear(ctx context.Context, sess *surface.Session) error {
	_, err := c.requestSession(ctx, sess, true, "thread/goal/clear", map[string]any{
		"threadId": sess.ID,
	}, 5*time.Second)
	return err
}

func (c *Codex) GoalGet(ctx context.Context, sess *surface.Session) (*surface.GoalState, error) {
	resp, err := c.requestSession(ctx, sess, false, "thread/goal/get", map[string]any{
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
	_, err := c.requestSession(ctx, sess, true, "thread/compact/start", map[string]any{
		"threadId": sess.ID,
	}, 10*time.Second)
	return err
}

func (c *Codex) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	params := map[string]any{"threadId": sess.ID}
	if name != "" {
		params["model"] = name
	}
	response, err := c.requestSession(ctx, sess, name != "", "thread/resume", params, 5*time.Second)
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
	conn, err := c.openDesktop(ctx)
	if err != nil && c.managed {
		conn, err = c.openManaged(ctx)
	}
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var models []surface.ModelOption
	var cursor any
	for page := 0; page < 20; page++ {
		params := map[string]any{"includeHidden": false, "limit": 100}
		if cursor != nil {
			params["cursor"] = cursor
		}
		response, err := conn.Request(ctx, "model/list", params, 10*time.Second)
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
	conn, err := c.openSession(ctx, sess, true)
	if err != nil {
		return err
	}
	defer conn.Close()
	turnID, err := c.activeTurnID(ctx, conn, sess.ID)
	if err != nil {
		return err
	}
	if turnID == "" {
		return fmt.Errorf("session idle; nothing to interrupt")
	}
	_, err = conn.Request(ctx, "turn/interrupt", map[string]any{
		"threadId": sess.ID,
	}, 5*time.Second)
	return err
}

func (c *Codex) Steer(ctx context.Context, sess *surface.Session, message string) error {
	conn, err := c.openSession(ctx, sess, true)
	if err != nil {
		return err
	}
	defer conn.Close()
	turnID, err := c.activeTurnID(ctx, conn, sess.ID)
	if err != nil {
		return err
	}
	if turnID == "" {
		return fmt.Errorf("session idle; nothing to steer (use 'send' instead)")
	}
	_, err = conn.Request(ctx, "turn/steer", map[string]any{
		"threadId":       sess.ID,
		"expectedTurnId": turnID,
		"input":          []map[string]any{{"type": "text", "text": message}},
	}, 5*time.Second)
	return err
}

var localHTTPClient = &http.Client{Timeout: 5 * time.Second}

func (c *Codex) Tail(ctx context.Context, sess *surface.Session, n int) ([]surface.Exchange, error) {
	conn, err := c.openSession(ctx, sess, false)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
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
