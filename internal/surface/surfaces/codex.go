package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

type Codex struct {
	desktopURL   string
	managed      bool
	bridgeMu     sync.Mutex
	bridgeTarget string
	bridgeErr    error
	bridgeRetry  time.Time
	runtimeMu    sync.Mutex
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
	return &Codex{desktopURL: remoteURL, managed: managed}
}

func (c *Codex) Name() surface.SurfaceKind { return surface.KindCodex }

func (c *Codex) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: true, Reply: true, Goal: true,
		Compact: true, Model: true, Interrupt: true, Steer: true,
	}
}

func (c *Codex) CatalogListComplete() bool { return false }

func (c *Codex) Health(ctx context.Context) error {
	if err := c.Ready(ctx); err != nil {
		return fmt.Errorf("Codex session discovery is unavailable: %w", err)
	}
	return nil
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
	ws     *websocket.Conn
	mu     sync.Mutex
	next   int
	target string
}

func (c *Codex) dial(ctx context.Context) (*cdpConn, error) {
	targets, err := resolveCodexRendererEndpoint(ctx, c.desktopURL)
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
		conn := &cdpConn{ws: ws, next: 1, target: target.wsURL}
		if probeErr := c.ensureDesktopHook(ctx, conn); probeErr == nil {
			return conn, nil
		} else {
			failures = append(failures, fmt.Sprintf("%s: Desktop app-server bridge unavailable (%v)", target.wsURL, probeErr))
		}
		_ = conn.close()
	}
	return nil, fmt.Errorf("connect Codex Desktop renderer bridge: %s", strings.Join(failures, "; "))
}

type codexCDPTarget struct {
	wsURL string
}

func resolveCodexRendererEndpoint(ctx context.Context, endpoint string) ([]codexCDPTarget, error) {
	httpURL := strings.Replace(endpoint, "ws://", "http://", 1)
	httpURL = strings.Replace(httpURL, "wss://", "https://", 1)
	for _, path := range []string{"/json/list", "/json"} {
		req, _ := http.NewRequestWithContext(ctx, "GET", httpURL+path, nil)
		resp, err := localHTTPClient.Do(req)
		if err != nil {
			continue
		}
		var values []map[string]any
		decodeErr := json.NewDecoder(resp.Body).Decode(&values)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || decodeErr != nil {
			continue
		}
		var primary []codexCDPTarget
		var secondary []codexCDPTarget
		for _, value := range values {
			if kind, _ := value["type"].(string); kind != "page" {
				continue
			}
			url, _ := value["url"].(string)
			wsURL, _ := value["webSocketDebuggerUrl"].(string)
			if wsURL == "" || !strings.HasPrefix(url, "app://-/index.html") {
				continue
			}
			target := codexCDPTarget{wsURL: wsURL}
			if strings.Contains(url, "avatar-overlay") {
				secondary = append(secondary, target)
			} else {
				primary = append(primary, target)
			}
		}
		if len(primary) > 0 {
			return append(primary, secondary...), nil
		}
	}
	return nil, fmt.Errorf("no Codex Desktop renderer debug target at %s; quit Codex and relaunch it with 'agenthail launch codex'", endpoint)
}

func (c *cdpConn) close() error { return c.ws.Close() }

func (c *cdpConn) evaluate(ctx context.Context, expr string, timeout time.Duration) (any, error) {
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
	return nil, fmt.Errorf("timeout waiting for eval response: %w", os.ErrDeadlineExceeded)
}

const codexRecentListLimit = 50

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
	if c.managed {
		if client, err := c.openExistingManaged(ctx); err == nil {
			clients = append(clients, struct {
				client           codexClient
				managed          bool
				desktopReachable bool
			}{client, true, false})
		} else if !desktopReachable {
			failures = append(failures, err.Error())
		}
	}
	if len(clients) == 0 {
		return nil, fmt.Errorf("Codex is unavailable: %s", strings.Join(failures, "; "))
	}
	byID := map[string]surface.Session{}
	succeeded := false
	for _, entry := range clients {
		sessions, err := c.listCurrent(ctx, entry.client, entry.managed, entry.desktopReachable)
		_ = entry.client.Close()
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		succeeded = true
		for _, session := range sessions {
			previous, exists := byID[session.ID]
			if !exists || codexTransportRank(session.Transport) > codexTransportRank(previous.Transport) {
				byID[session.ID] = session
			}
		}
	}
	if !succeeded {
		return nil, fmt.Errorf("thread/list: %s", strings.Join(failures, "; "))
	}
	out := make([]surface.Session, 0, len(byID))
	for _, session := range byID {
		c.reconcileLocalStatus(&session)
		out = append(out, session)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out, nil
}

func codexTransportRank(transport string) int {
	switch transport {
	case codexTransportDesktop:
		return 3
	case codexTransportManaged:
		return 2
	default:
		return 1
	}
}

func (c *Codex) Ready(ctx context.Context) error {
	conn, _, _, err := c.openDiscovery(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = c.loadedThreadIDs(ctx, conn, 1)
	return err
}

func (c *Codex) DesktopReady(ctx context.Context) error {
	client, err := c.openDesktop(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	_, err = c.loadedThreadIDs(ctx, client, 1)
	return err
}

func (c *Codex) listCurrent(ctx context.Context, conn codexClient, managed, desktopReachable bool) ([]surface.Session, error) {
	loaded, err := c.listLoaded(ctx, conn, codexRecentListLimit, managed, desktopReachable)
	if err != nil {
		return nil, err
	}
	recent, err := c.listRecent(ctx, conn, false, desktopReachable)
	if err != nil {
		return loaded, nil
	}
	return mergeCodexSessions(loaded, recent), nil
}

func (c *Codex) listLoaded(ctx context.Context, conn codexClient, limit int, managed, desktopReachable bool) ([]surface.Session, error) {
	ids, err := c.loadedThreadIDs(ctx, conn, limit)
	if err != nil {
		return nil, err
	}
	sessions := make([]surface.Session, 0, len(ids))
	var readErrors []string
	for _, id := range ids {
		session, err := c.readSession(ctx, conn, id, false, false)
		if err != nil {
			readErrors = append(readErrors, err.Error())
			continue
		}
		if desktopReachable {
			session.Transport = codexTransportDesktop
		} else if managed {
			session.Transport = codexTransportManaged
		}
		sessions = append(sessions, session)
	}
	if len(sessions) == 0 && len(ids) > 0 && len(readErrors) > 0 {
		return nil, fmt.Errorf("read loaded Codex threads: %s", strings.Join(readErrors, "; "))
	}
	return sessions, nil
}

func (c *Codex) loadedThreadIDs(ctx context.Context, conn codexClient, limit int) ([]string, error) {
	resp, err := conn.Request(ctx, "thread/loaded/list", map[string]any{"limit": limit}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	result, _ := resp["result"].(map[string]any)
	values, _ := result["data"].([]any)
	ids := make([]string, 0, len(values))
	for _, value := range values {
		id := loadedThreadID(value)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func loadedThreadID(value any) string {
	if id, ok := value.(string); ok {
		return id
	}
	entry, _ := value.(map[string]any)
	if id := str(entry, "id"); id != "" {
		return id
	}
	thread, _ := entry["thread"].(map[string]any)
	return str(thread, "id")
}

func mergeCodexSessions(primary, secondary []surface.Session) []surface.Session {
	byID := make(map[string]surface.Session, len(primary)+len(secondary))
	for _, session := range secondary {
		byID[session.ID] = session
	}
	for _, session := range primary {
		byID[session.ID] = session
	}
	out := make([]surface.Session, 0, len(byID))
	for _, session := range byID {
		out = append(out, session)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out
}

func (c *Codex) listRecent(ctx context.Context, conn codexClient, managed, desktopReachable bool) ([]surface.Session, error) {
	return c.listPage(ctx, conn, map[string]any{
		"limit":          codexRecentListLimit,
		"sortKey":        "recency_at",
		"sortDirection":  "desc",
		"useStateDbOnly": true,
	}, managed, desktopReachable)
}

func (c *Codex) listPage(ctx context.Context, conn codexClient, params map[string]any, managed, desktopReachable bool) ([]surface.Session, error) {
	resp, err := conn.Request(ctx, "thread/list", params, 10*time.Second)
	if err != nil {
		return nil, err
	}
	result, _ := resp["result"].(map[string]any)
	threads, _ := result["data"].([]any)
	sessions := make([]surface.Session, 0, len(threads))
	for _, value := range threads {
		thread, _ := value.(map[string]any)
		sessions = append(sessions, codexSession(thread, managed, desktopReachable))
	}
	return sessions, nil
}

func codexSession(thread map[string]any, managed, desktopReachable bool) surface.Session {
	source := codexSource(thread["source"])
	session := surface.Session{
		ID:        str(thread, "id"),
		Surface:   surface.KindCodex,
		Name:      surface.DeriveName(str(thread, "name"), str(thread, "preview"), 60),
		Cwd:       str(thread, "cwd"),
		Status:    codexStatus(thread["status"]),
		Source:    source,
		Transport: codexTransport(managed, desktopReachable),
	}
	if timestamp, ok := thread["recencyAt"].(float64); ok && timestamp > 0 {
		session.LastActive = time.Unix(int64(timestamp), 0)
	}
	return session
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
		return c.resolveID(ctx, target)
	}
	results, err := c.SearchSessions(ctx, target, 20)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(target)
	var matches []surface.Session
	var exactMatches []surface.Session
	for _, result := range results {
		session := result.Session
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

func (c *Codex) resolveID(ctx context.Context, id string) (*surface.Session, error) {
	transports := c.loadedTransports(ctx)
	transport := transports[id]
	var conn codexClient
	var err error
	var desktopReachable bool
	switch transport {
	case codexTransportDesktop:
		conn, err = c.openDesktop(ctx)
		desktopReachable = true
	case codexTransportManaged:
		conn, err = c.openExistingManaged(ctx)
	default:
		conn, _, desktopReachable, err = c.openDiscovery(ctx)
	}
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	session, err := c.readSession(ctx, conn, id, false, desktopReachable)
	if err != nil && transport == "" && c.managed {
		if _, desktop := conn.(*desktopCodexClient); desktop {
			managed, managedErr := c.openExistingManaged(ctx)
			if managedErr == nil {
				defer managed.Close()
				session, err = c.readSession(ctx, managed, id, false, false)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if transport != "" {
		session.Transport = transport
	}
	if session.Transport == "" {
		session.Transport = codexTransportReadOnly
	}
	return &session, nil
}

func (c *Codex) readSession(ctx context.Context, conn codexClient, id string, managed, desktopReachable bool) (surface.Session, error) {
	response, err := conn.Request(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, 5*time.Second)
	if err != nil {
		return surface.Session{}, fmt.Errorf("thread/read: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	if thread == nil {
		return surface.Session{}, fmt.Errorf("thread/read response missing thread")
	}
	session := codexSession(thread, managed, desktopReachable)
	if session.ID == "" {
		session.ID = id
	}
	return session, nil
}

func (c *Codex) SearchSessions(ctx context.Context, query string, limit int) ([]surface.SessionSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("Codex history search requires a query")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	conn, managed, desktopReachable, err := c.openDiscovery(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	params := map[string]any{
		"searchTerm":    query,
		"limit":         limit,
		"sortKey":       "recency_at",
		"sortDirection": "desc",
		"archived":      false,
	}
	response, err := conn.Request(ctx, "thread/search", params, 5*time.Second)
	if err != nil && c.managed {
		if _, desktop := conn.(*desktopCodexClient); desktop {
			managedConn, managedErr := c.openExistingManaged(ctx)
			if managedErr == nil {
				defer managedConn.Close()
				response, err = managedConn.Request(ctx, "thread/search", params, 5*time.Second)
				managed, desktopReachable = true, false
			}
		}
	}
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "method not found") || strings.Contains(err.Error(), "-32601") {
			return nil, surface.ErrUnsupported
		}
		return nil, fmt.Errorf("Codex history search: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	values, _ := result["data"].([]any)
	output := make([]surface.SessionSearchResult, 0, len(values))
	for _, value := range values {
		entry, _ := value.(map[string]any)
		thread, _ := entry["thread"].(map[string]any)
		if thread == nil {
			thread = entry
		}
		session := codexSession(thread, managed, desktopReachable)
		if session.ID == "" {
			continue
		}
		output = append(output, surface.SessionSearchResult{Session: session, Snippet: str(entry, "snippet")})
	}
	transports := c.loadedTransports(ctx)
	for index := range output {
		if transport := transports[output[index].Session.ID]; transport != "" {
			output[index].Session.Transport = transport
		}
		if output[index].Session.Transport == "" {
			output[index].Session.Transport = codexTransportReadOnly
		}
	}
	return output, nil
}

func (c *Codex) openDiscovery(ctx context.Context) (codexClient, bool, bool, error) {
	if client, err := c.openDesktop(ctx); err == nil {
		return client, false, true, nil
	}
	client, err := c.openExistingManaged(ctx)
	return client, true, false, err
}

func (c *Codex) loadedTransports(ctx context.Context) map[string]string {
	transports := make(map[string]string)
	if desktop, err := c.openDesktop(ctx); err == nil {
		if ids, listErr := c.loadedThreadIDs(ctx, desktop, 100); listErr == nil {
			for _, id := range ids {
				transports[id] = codexTransportDesktop
			}
		}
		_ = desktop.Close()
	}
	if !c.managed {
		return transports
	}
	if managed, err := c.openExistingManaged(ctx); err == nil {
		if ids, listErr := c.loadedThreadIDs(ctx, managed, 100); listErr == nil {
			for _, id := range ids {
				if _, desktopOwns := transports[id]; !desktopOwns {
					transports[id] = codexTransportManaged
				}
			}
		}
		_ = managed.Close()
	}
	return transports
}

func (c *Codex) Observe(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	conn, err := c.openSession(ctx, sess, false)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	thread, err := c.readObservationThread(ctx, conn, sess.ID)
	if err != nil {
		return nil, err
	}
	return codexObservation(thread), nil
}

func (c *Codex) activeTurnID(ctx context.Context, conn codexClient, threadID string) (string, error) {
	response, err := conn.Request(ctx, "thread/turns/list", map[string]any{
		"threadId":      threadID,
		"page":          map[string]any{"limit": 1},
		"sortDirection": "desc",
	}, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("thread/turns/list: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	turns, _ := result["data"].([]any)
	for _, raw := range turns {
		turn, _ := raw.(map[string]any)
		status, _, _ := codexTurnState(turn["status"])
		if status == surface.StatusBusy {
			return str(turn, "id"), nil
		}
	}
	return "", nil
}

func (c *Codex) Send(ctx context.Context, sess *surface.Session, message string) (*surface.SendResult, error) {
	return c.SendWithOptions(ctx, sess, message, surface.SendOptions{})
}

func (c *Codex) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return nil, nil, surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	switch options.Owner {
	case "", codexTransportDesktop:
		client, err := c.openDesktop(ctx)
		if err != nil {
			return nil, nil, surface.DeliveryUnavailable(fmt.Errorf("Codex Desktop is unavailable for Desktop-owned creation: %w", err))
		}
		defer client.Close()
		return c.startSessionOnTransport(ctx, client, options, codexTransportDesktop)
	case codexTransportManaged:
		return nil, nil, fmt.Errorf("managed Codex conversations must be started with 'agenthail codex' so their terminal remains attached")
	default:
		return nil, nil, fmt.Errorf("Codex session owner must be %q or %q", codexTransportDesktop, codexTransportManaged)
	}
}

func (c *Codex) startSession(ctx context.Context, client codexClient, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	return c.startSessionOnTransport(ctx, client, options, codexTransportManaged)
}

func (c *Codex) startSessionOnTransport(ctx context.Context, client codexClient, options surface.SessionStartOptions, transport string) (*surface.Session, *surface.SendResult, error) {
	if err := options.TurnOptions.Validate(surface.KindCodex); err != nil {
		return nil, nil, err
	}
	if options.Worktree != "" || options.Agent != "" || options.PermissionMode != "" || options.Name != "" {
		return nil, nil, fmt.Errorf("name, worktree, agent and permission-mode creation options require Claude")
	}
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
		Transport:  transport,
		LastActive: time.Now(),
	}
	if session.Cwd == "" {
		session.Cwd = options.Cwd
	}
	turnParams := map[string]any{
		"threadId": threadID,
		"input":    []map[string]any{{"type": "text", "text": message}},
	}
	if err := applyCodexTurnOptions(ctx, client, session, options.Model, options.TurnOptions, turnParams); err != nil {
		return session, nil, err
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
	if err := options.TurnOptions.Validate(surface.KindCodex); err != nil {
		return nil, surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	conn, err := c.openSession(ctx, sess, true)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	defer conn.Close()
	if err := c.requireDirectInput(ctx, conn, sess); err != nil {
		var notWritable *codexNotWritableError
		if errors.As(err, &notWritable) {
			return nil, surface.DeliveryTerminal(err, surface.DeliveryOwnershipConflict)
		}
		return nil, surface.DeliveryUnavailable(err)
	}
	active, err := c.activeTurnID(ctx, conn, sess.ID)
	if err != nil {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("inspect active turn: %w", err))
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
	if err := applyCodexTurnOptions(ctx, conn, sess, options.Model, options.TurnOptions, params); err != nil {
		return nil, surface.DeliveryUnavailable(err)
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

func codexDirectInputAccepted(response map[string]any, explicit bool) bool {
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	if thread == nil {
		return false
	}
	if accepts, present := thread["canAcceptDirectInput"].(bool); present {
		return accepts
	}
	return !explicit
}

func isCodexTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "app-server request timed out")
}

func (c *Codex) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	read, err := c.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: max(1, limit)})
	if err != nil {
		return nil, err
	}
	if read.Reply == nil {
		return &surface.ReplyResult{Done: false, Source: read.Source}, nil
	}
	return read.Reply, nil
}

func (c *Codex) Stream(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	if sess.Transport == codexTransportManaged {
		return c.streamManaged(ctx, sess, uuid, onEvent, timeout)
	}
	client, err := c.openDesktop(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	if uuid != "" {
		thread, readErr := c.readObservationThread(ctx, client, sess.ID)
		if readErr != nil {
			return readErr
		}
		if turn := codexTurnByID(thread, uuid); turn != nil && turn.Done {
			if turn.Error != "" {
				return fmt.Errorf("Codex turn %s did not complete successfully: %s", uuid, turn.Error)
			}
			if assistant, ok := turn.authoritativeAssistant(); ok {
				onEvent(codexAuthoritativeStreamEvent(turn.ID, assistant))
			}
			onEvent(surface.StreamEvent{ID: "codex:" + turn.ID + ":done", ProviderKey: "codex:" + turn.ID + ":done", Operation: "phase", TurnID: turn.ID, Kind: "done"})
			return nil
		}
	}
	desktop, ok := client.(*desktopCodexClient)
	if !ok {
		return fmt.Errorf("Codex Desktop stream requires the Desktop transport")
	}
	cursorValue, err := desktop.conn.evaluate(ctx, codexEventCursorJS, 2*time.Second)
	if err != nil {
		return err
	}
	cursor, _ := cursorValue.(float64)
	if uuid != "" {
		cursor = 0
	}
	emittedText := ""
	desktopState := codexDesktopStreamState{turnID: uuid}
	var lastContext surface.ContextUsage
	var nextContextPoll time.Time
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		v, err := desktop.conn.evaluate(ctx, codexEventsJS(int64(cursor)), 2*time.Second)
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
			if goalEvent, ok := codexGoalStreamEvent(event); ok {
				onEvent(goalEvent)
				continue
			}
			if uuid == "" {
				desktopState.observe(event)
			}
			if usage, ok := c.applyContextEvent(sess, event, lastContext); ok {
				lastContext = *usage
				onEvent(codexStreamEvent(event.Sequence, "context", "", usage, uuid))
				continue
			}
			if uuid != "" && !codexContainsID(event.Params, uuid) {
				continue
			}
			method := event.Method
			switch {
			case strings.Contains(strings.ToLower(method), "agentmessage"):
				if txt := codexEventText(event.Params); txt != "" {
					if uuid == "" {
						if textEvent, ok := desktopState.textEvent(txt, codexEventItemID(event.Params)); ok {
							onEvent(textEvent)
						}
					} else {
						emittedText += txt
						onEvent(codexStreamEvent(event.Sequence, "text", txt, nil, uuid))
					}
				}
			case strings.Contains(strings.ToLower(method), "tool"):
				if name := codexEventTool(event.Params); name != "" {
					if uuid == "" {
						if toolEvent, ok := desktopState.toolEvent(event.Sequence, name, codexEventItemID(event.Params), codexEventItemBody(codexEventItem(event.Params))); ok {
							onEvent(toolEvent)
						}
					} else {
						onEvent(codexStreamEvent(event.Sequence, "tool_use", name, nil, uuid))
					}
				}
			case uuid == "" && strings.Contains(strings.ToLower(method), "item"):
				if itemEvent, ok := desktopState.itemEvent(event); ok {
					onEvent(itemEvent)
				}
			case codexCompletionMethod(method):
				thread, readErr := c.readObservationThread(ctx, client, sess.ID)
				if readErr != nil {
					return readErr
				}
				turnID := uuid
				if turnID == "" {
					turnID = desktopState.turnID
				}
				turn := codexTurnByID(thread, turnID)
				if turn != nil && turn.Error != "" {
					return fmt.Errorf("Codex turn %s did not complete successfully: %s", turnID, turn.Error)
				}
				if turn != nil {
					if uuid == "" {
						if finalEvent, ok := desktopState.finalEvent(turnID, turn); ok {
							onEvent(finalEvent)
						}
					} else {
						for _, streamEvent := range codexCompletionStreamEvents(event.Sequence, uuid, turn, emittedText) {
							onEvent(streamEvent)
						}
					}
				}
				if uuid == "" {
					if doneEvent, ok := desktopState.doneEvent(event.Sequence, turnID); ok {
						onEvent(doneEvent)
					}
				} else {
					onEvent(codexStreamEvent(event.Sequence, "done", "", nil, uuid))
				}
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

type codexDesktopStreamState struct {
	turnID        string
	assistantKey  string
	assistantItem string
	textByItem    map[string]string
}

func (s *codexDesktopStreamState) observe(event codexEvent) {
	turnID := codexEventTurnID(event.Params)
	if turnID == "" || turnID == s.turnID {
		return
	}
	s.turnID = turnID
	s.assistantKey = ""
	s.assistantItem = ""
	s.textByItem = make(map[string]string)
}

func (s *codexDesktopStreamState) textEvent(text, itemID string) (surface.StreamEvent, bool) {
	if s.turnID == "" {
		return surface.StreamEvent{}, false
	}
	if s.textByItem == nil {
		s.textByItem = make(map[string]string)
	}
	if itemID != "" && itemID != s.assistantItem {
		s.assistantItem = itemID
		s.assistantKey = codexDesktopStreamKey(s.turnID, "assistant", itemID)
	} else if s.assistantKey == "" {
		s.assistantKey = codexDesktopStreamKey(s.turnID, "assistant", "")
	}
	s.textByItem[s.assistantKey] += text
	return surface.StreamEvent{ID: s.assistantKey, ProviderKey: s.assistantKey, Version: uint64(len(s.textByItem[s.assistantKey])), Operation: "append", TurnID: s.turnID, Kind: "text", Text: text}, true
}

func (s *codexDesktopStreamState) toolEvent(sequence int64, name, itemID, body string) (surface.StreamEvent, bool) {
	if s.turnID == "" {
		return surface.StreamEvent{}, false
	}
	if itemID == "" {
		itemID = strconv.FormatInt(sequence, 10)
	}
	key := codexDesktopStreamKey(s.turnID, "tool", itemID)
	if body == "" {
		body = name
	}
	return surface.StreamEvent{ID: key, ProviderKey: key, Version: 1, Operation: "upsert", TurnID: s.turnID, Kind: "tool_use", Title: name, Text: body}, true
}

func (s *codexDesktopStreamState) itemEvent(event codexEvent) (surface.StreamEvent, bool) {
	item := codexEventItem(event.Params)
	if item == nil {
		return surface.StreamEvent{}, false
	}
	itemID := codexEventItemID(item)
	if itemID == "" {
		itemID = strconv.FormatInt(event.Sequence, 10)
	}
	typ := strings.ToLower(str(item, "type"))
	kind, role := "", ""
	switch {
	case strings.Contains(typ, "usermessage") || typ == "user":
		kind, role = "message", "user"
	case strings.Contains(typ, "reason"):
		kind, role = "reasoning", "assistant"
	case strings.Contains(typ, "functioncall") || strings.Contains(typ, "toolcall"):
		kind, role = "tool_use", "assistant"
	case strings.Contains(typ, "functioncalloutput") || strings.Contains(typ, "toolresult"):
		kind, role = "tool_result", "assistant"
	case strings.Contains(typ, "agentmessage") || typ == "assistant":
		kind, role = "text", "assistant"
	default:
		return surface.StreamEvent{}, false
	}
	turnID := s.turnID
	if eventTurnID := codexEventTurnID(event.Params); eventTurnID != "" {
		turnID = eventTurnID
	}
	if turnID == "" {
		return surface.StreamEvent{}, false
	}
	body := codexEventItemBody(item)
	if body == "" {
		return surface.StreamEvent{}, false
	}
	key := codexDesktopStreamKey(turnID, kind, itemID)
	result := surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(len(body)), Operation: "upsert", TurnID: turnID, Kind: kind, Role: role, Title: str(item, "name"), Text: body, Status: str(item, "status")}
	if kind == "text" && str(item, "phase") == "final_answer" {
		result.Final = true
	}
	return result, true
}

func (s *codexDesktopStreamState) finalEvent(turnID string, turn *codexTurn) (surface.StreamEvent, bool) {
	if turnID == "" {
		return surface.StreamEvent{}, false
	}
	assistant, ok := turn.authoritativeAssistant()
	if !ok || assistant.Text == "" {
		return surface.StreamEvent{}, false
	}
	key := codexDesktopStreamKey(turnID, "assistant", assistant.ID)
	if assistant.ID == "" && s.assistantKey != "" {
		key = s.assistantKey
	}
	return surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(len(assistant.Text)), Operation: "upsert", Final: true, TurnID: turnID, Kind: "text", Text: assistant.Text}, true
}

func (s *codexDesktopStreamState) doneEvent(sequence int64, turnID string) (surface.StreamEvent, bool) {
	if turnID == "" {
		return surface.StreamEvent{}, false
	}
	key := codexDesktopStreamKey(turnID, "done", "")
	return surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(sequence), Operation: "phase", TurnID: turnID, Kind: "done"}, true
}

func codexDesktopStreamKey(turnID, kind, itemID string) string {
	if turnID == "" {
		return "renderer:" + kind + ":" + itemID
	}
	key := "codex:" + turnID + ":" + kind
	if itemID != "" {
		key += ":" + itemID
	}
	return key
}

func (c *Codex) streamManaged(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	client, err := c.openSession(ctx, sess, false)
	if err != nil {
		return err
	}
	defer client.Close()
	return c.streamManagedClient(ctx, client, sess, uuid, onEvent, timeout)
}

func codexStreamEvent(sequence int64, kind, text string, contextUsage *surface.ContextUsage, turnID string) surface.StreamEvent {
	key := fmt.Sprintf("renderer:%d", sequence)
	operation := "append"
	if kind == "context" || kind == "done" {
		operation = "upsert"
	}
	return surface.StreamEvent{ID: key, ProviderKey: key, Version: 1, Operation: operation, TurnID: turnID, Kind: kind, Text: text, Context: contextUsage}
}

func codexAuthoritativeStreamEvent(turnID string, item codexAssistantItem) surface.StreamEvent {
	return codexAuthoritativeStreamEventWithKey("codex:"+turnID+":assistant", turnID, item)
}

func codexManagedAuthoritativeStreamEvent(turnID string, item codexAssistantItem) surface.StreamEvent {
	return codexAuthoritativeStreamEventWithKey("managed:"+turnID+":text", turnID, item)
}

func codexAuthoritativeStreamEventWithKey(key, turnID string, item codexAssistantItem) surface.StreamEvent {
	if item.ID != "" {
		if strings.HasPrefix(key, "codex:") {
			key += ":" + item.ID
		}
	}
	return surface.StreamEvent{ID: key, ProviderKey: key, Version: uint64(len(item.Text)), Operation: "upsert", Final: true, TurnID: turnID, Kind: "text", Text: item.Text}
}

func codexCompletionStreamEvents(sequence int64, turnID string, turn *codexTurn, emitted string) []surface.StreamEvent {
	assistant, ok := turn.authoritativeAssistant()
	if !ok || assistant.Text == "" {
		return nil
	}
	events := make([]surface.StreamEvent, 0, 2)
	if emitted != "" && assistant.Text != emitted && strings.HasPrefix(assistant.Text, emitted) {
		events = append(events, codexStreamEvent(sequence, "text", strings.TrimPrefix(assistant.Text, emitted), nil, turnID))
	}
	events = append(events, codexAuthoritativeStreamEvent(turnID, assistant))
	return events
}

func (c *Codex) streamManagedClient(ctx context.Context, client codexClient, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	emitted := ""
	baselineTurnID := ""
	finalEmitted := false
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
				if goalEvent, ok := codexGoalStreamEvent(event); ok {
					onEvent(goalEvent)
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
		if turn != nil {
			if turn.Done && turn.Error != "" {
				return fmt.Errorf("Codex turn %s did not complete successfully: %s", turn.ID, turn.Error)
			}
			assistant, hasAssistant := turn.authoritativeAssistant()
			if hasAssistant && turn.Done {
				if !finalEmitted {
					onEvent(codexManagedAuthoritativeStreamEvent(turn.ID, assistant))
					finalEmitted = true
				}
				emitted = assistant.Text
			} else if hasAssistant && strings.HasPrefix(assistant.Text, emitted) {
				delta := strings.TrimPrefix(assistant.Text, emitted)
				if delta != "" {
					emitted = assistant.Text
					key := "managed:" + turn.ID
					onEvent(surface.StreamEvent{ID: key + ":text", ProviderKey: key + ":text", Version: uint64(len(assistant.Text)), Operation: "append", TurnID: turn.ID, Kind: "text", Text: delta})
				}
			}
			if turn.Done {
				key := "managed:" + turn.ID
				version := uint64(len(turn.Assistant))
				if hasAssistant {
					version = uint64(len(assistant.Text))
				}
				onEvent(surface.StreamEvent{ID: key + ":done", ProviderKey: key + ":done", Version: version, Operation: "phase", TurnID: turn.ID, Kind: "done"})
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

func (c *Codex) GoalClear(ctx context.Context, sess *surface.Session) error {
	_, err := c.requestSession(ctx, sess, true, "thread/goal/clear", map[string]any{
		"threadId": sess.ID,
	}, 5*time.Second)
	return err
}

func (c *Codex) Compact(ctx context.Context, sess *surface.Session) error {
	_, err := c.requestSession(ctx, sess, true, "thread/compact/start", map[string]any{
		"threadId": sess.ID,
	}, 10*time.Second)
	return err
}

func (c *Codex) Model(ctx context.Context, sess *surface.Session, name string) (string, error) {
	if name == "" {
		response, err := c.requestSession(ctx, sess, false, "thread/read", map[string]any{"threadId": sess.ID, "includeTurns": false}, 2*time.Second)
		if err != nil {
			return "", err
		}
		result, _ := response["result"].(map[string]any)
		thread, _ := result["thread"].(map[string]any)
		model := str(thread, "model")
		if model == "" {
			model = str(result, "model")
		}
		if model == "" {
			return "", fmt.Errorf("session metadata does not include the active model")
		}
		return model, nil
	}
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
	return listCodexModels(ctx, conn)
}

func listCodexModels(ctx context.Context, conn codexClient) ([]surface.ModelOption, error) {
	var models []surface.ModelOption
	seen := make(map[string]struct{})
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{"includeHidden": false, "limit": 100}
		if cursor != "" {
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
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			displayName := str(value, "displayName")
			if displayName == "" {
				displayName = id
			}
			models = append(models, surface.ModelOption{
				ID:                        id,
				DisplayName:               displayName,
				Description:               str(value, "description"),
				Default:                   value["isDefault"] == true,
				SupportedReasoningEfforts: stringList(value["supportedReasoningEfforts"], "reasoningEffort"),
				DefaultReasoningEffort:    str(value, "defaultReasoningEffort"),
				ServiceTiers:              stringList(value["serviceTiers"], "id"),
			})
		}
		nextCursor, _ := result["nextCursor"].(string)
		if nextCursor == "" || nextCursor == cursor {
			break
		}
		cursor = nextCursor
	}
	return models, nil
}

func stringList(value any, objectKey string) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		switch item := item.(type) {
		case string:
			if item != "" {
				result = append(result, item)
			}
		case map[string]any:
			if item, ok := item[objectKey].(string); ok && item != "" {
				result = append(result, item)
			}
		}
	}
	return result
}

func (c *Codex) Interrupt(ctx context.Context, sess *surface.Session) error {
	return c.interruptTurn(ctx, sess, "")
}

func (c *Codex) InterruptTurn(ctx context.Context, sess *surface.Session, expectedTurnID string) error {
	return c.interruptTurn(ctx, sess, expectedTurnID)
}

func (c *Codex) interruptTurn(ctx context.Context, sess *surface.Session, expectedTurnID string) error {
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	conn, err := c.openSession(ctx, sess, true)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer conn.Close()
	if err := c.requireDirectInput(ctx, conn, sess); err != nil {
		return surface.DeliveryUnavailable(err)
	}
	return c.interruptActiveTurn(ctx, conn, sess.ID, expectedTurnID)
}

func (c *Codex) interruptActiveTurn(ctx context.Context, conn codexClient, threadID, expectedTurnID string) error {
	turnID, err := c.activeTurnID(ctx, conn, threadID)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	if turnID == "" {
		return fmt.Errorf("session idle; nothing to interrupt")
	}
	if expectedTurnID != "" && turnID != expectedTurnID {
		return fmt.Errorf("selected turn changed before interruption; confirm the active turn again")
	}
	_, err = conn.Request(ctx, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	}, 5*time.Second)
	return err
}

func (c *Codex) Steer(ctx context.Context, sess *surface.Session, message string) error {
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	conn, err := c.openSession(ctx, sess, true)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer conn.Close()
	if err := c.requireDirectInput(ctx, conn, sess); err != nil {
		return surface.DeliveryUnavailable(err)
	}
	turnID, err := c.activeTurnID(ctx, conn, sess.ID)
	if err != nil {
		return surface.DeliveryUnavailable(err)
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
	read, err := c.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: n})
	if err != nil {
		return nil, err
	}
	return read.Exchanges, nil
}

func (c *Codex) readSessionFromRPC(ctx context.Context, sess *surface.Session, n int) (*surface.SessionReadResult, error) {
	conn, err := c.openSession(ctx, sess, false)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	thread, err := c.readThreadWithOptions(ctx, conn, sess.ID, n, true)
	if err != nil {
		return nil, err
	}

	var exchanges []surface.Exchange
	for _, turn := range thread.Turns {
		if turn.User != "" || turn.Assistant != "" {
			exchanges = append(exchanges, surface.Exchange{User: turn.User, Assistant: turn.Assistant, Source: "rpc"})
		}
	}

	if len(exchanges) > n {
		exchanges = exchanges[len(exchanges)-n:]
	}
	return &surface.SessionReadResult{Items: []surface.TimelineItem{}, Exchanges: exchanges, Reply: latestRPCReply(sess, exchanges, thread), Source: "rpc"}, nil
}

func latestRPCReply(session *surface.Session, exchanges []surface.Exchange, thread *codexThread) *surface.ReplyResult {
	for index := len(thread.Turns) - 1; index >= 0; index-- {
		turn := thread.Turns[index]
		if turn.Assistant != "" || turn.Error != "" || turn.Status == surface.StatusBusy {
			return &surface.ReplyResult{Text: turn.Assistant, UserText: turn.User, Done: turn.Done, Error: turn.Error, Source: "rpc"}
		}
	}
	for index := len(exchanges) - 1; index >= 0; index-- {
		if exchanges[index].Assistant != "" {
			return &surface.ReplyResult{Text: exchanges[index].Assistant, UserText: exchanges[index].User, Done: session == nil || session.Status != surface.StatusBusy, Source: "rpc"}
		}
	}
	return &surface.ReplyResult{Done: false, Source: "rpc"}
}
