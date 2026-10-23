package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

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
	sweepMu      sync.Mutex
	lastSweep    time.Time
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

func (c *Codex) RuntimeKinds() []surface.RuntimeKind {
	return []surface.RuntimeKind{surface.RuntimeCodexDesktop, surface.RuntimeCodexAppServer}
}

// setCodexTransport routes a session and records the runtime that carries it:
// the managed app-server for managed threads, Codex Desktop for the rest,
// including read-only threads whose Desktop bridge is unavailable.
func setCodexTransport(session *surface.Session, transport string) {
	session.Transport = transport
	session.RuntimeKind = surface.RuntimeCodexDesktop
	if transport == codexTransportManaged {
		session.RuntimeKind = surface.RuntimeCodexAppServer
	}
}

func (c *Codex) RequiresLocalTranscript(session *surface.Session) bool {
	return session != nil && !c.managed && session.Transport != codexTransportManaged
}

func (c *Codex) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: true, Reply: true, Goal: true, GoalSet: true, GoalStatus: true,
		Compact: true, Model: true, Interrupt: true, Steer: true,
		Attach: true,
	}
}

// CatalogList lists loaded and recent threads on every pass. On the first
// pass and then every codexCatalogSweepInterval it pages through every thread
// Codex holds; only such a sweep is complete, so only it can retire threads
// Codex no longer has.
func (c *Codex) CatalogList(ctx context.Context) ([]surface.Session, bool, error) {
	c.sweepMu.Lock()
	sweep := c.lastSweep.IsZero() || time.Since(c.lastSweep) >= codexCatalogSweepInterval
	c.sweepMu.Unlock()
	started := time.Now()
	sessions, complete, err := c.list(ctx, sweep)
	if err == nil && complete {
		c.sweepMu.Lock()
		c.lastSweep = started
		c.sweepMu.Unlock()
	}
	return sessions, complete, err
}

// CatalogWatchDirs are the folders where a new thread's rollout file
// appears: Codex's sessions folder and its newest year, month and day
// folders. Watching each level means the folder for a new day is watched as
// soon as Codex creates it, before its first rollout lands.
func (c *Codex) CatalogWatchDirs() []string {
	dir := filepath.Join(codexHome(), "sessions")
	dirs := []string{dir}
	for range 3 {
		entries, err := os.ReadDir(dir)
		if err != nil {
			break
		}
		newest := ""
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() > newest {
				newest = entry.Name()
			}
		}
		if newest == "" {
			break
		}
		dir = filepath.Join(dir, newest)
		dirs = append(dirs, dir)
	}
	return dirs
}

// Presence counts a Codex session current while it works, has queued messages,
// or was active within the dashboard's Codex recency window, unless Codex has
// not loaded it.
func (c *Codex) Presence(sess surface.Session, input surface.PresenceInput) (bool, string) {
	if sess.Status == surface.StatusBusy {
		return true, "working"
	}
	if input.QueueCount > 0 {
		return true, "queued"
	}
	if sess.Status == surface.SessionStatus("notLoaded") || sess.LastActive.IsZero() {
		return false, ""
	}
	if input.Now.Sub(sess.LastActive) <= time.Duration(input.CodexRecentHours)*time.Hour {
		return true, "recent"
	}
	return false, ""
}

func (c *Codex) Health(ctx context.Context) error {
	if err := c.Ready(ctx); err != nil {
		return fmt.Errorf("Codex session discovery is unavailable: %w", err)
	}
	return nil
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

const (
	codexRecentListLimit      = 50
	codexSweepPageLimit       = 100
	codexCatalogSweepInterval = 5 * time.Minute
)

func (c *Codex) List(ctx context.Context) ([]surface.Session, error) {
	sessions, _, err := c.list(ctx, false)
	return sessions, err
}

// list reports whether every runtime returned every thread it holds, which
// only a sweep can.
func (c *Codex) list(ctx context.Context, sweep bool) ([]surface.Session, bool, error) {
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	clients := runtimes.readers()
	if len(clients) == 0 {
		return nil, false, runtimes.unavailable()
	}
	byID := map[string]surface.Session{}
	var failures []string
	succeeded := false
	complete := sweep
	for _, client := range clients {
		var sessions []surface.Session
		var err error
		if sweep {
			sessions, err = c.listAll(ctx, client, runtimes)
		} else {
			sessions, err = c.listCurrent(ctx, client, runtimes)
		}
		if err != nil {
			failures = append(failures, err.Error())
			complete = false
			continue
		}
		succeeded = true
		for _, session := range sessions {
			if previous, exists := byID[session.ID]; !exists || session.LastActive.After(previous.LastActive) {
				byID[session.ID] = session
			}
		}
	}
	if !succeeded {
		return nil, false, fmt.Errorf("thread/list: %s", strings.Join(failures, "; "))
	}
	out := make([]surface.Session, 0, len(byID))
	for _, session := range byID {
		c.reconcileLocalStatus(&session)
		applyCodexPendingModel(&session)
		out = append(out, session)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out, complete, nil
}

func (c *Codex) Ready(ctx context.Context) error {
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	clients := runtimes.readers()
	if len(clients) == 0 {
		return runtimes.unavailable()
	}
	_, err := c.loadedThreadIDs(ctx, clients[0], 1)
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

func (c *Codex) listCurrent(ctx context.Context, conn codexClient, runtimes *codexRuntimes) ([]surface.Session, error) {
	loaded, err := c.listLoaded(ctx, conn, codexRecentListLimit, runtimes)
	if err != nil {
		return nil, err
	}
	recent, err := c.listRecent(ctx, conn, runtimes)
	if err != nil {
		return loaded, nil
	}
	return mergeCodexSessions(loaded, recent), nil
}

// listAll returns every loaded thread and every page of the thread list. A
// failed read or page fails the sweep, since a partial sweep cannot retire
// threads.
func (c *Codex) listAll(ctx context.Context, conn codexClient, runtimes *codexRuntimes) ([]surface.Session, error) {
	loaded, readErrors, err := c.readLoaded(ctx, conn, 0, runtimes)
	if err != nil {
		return nil, err
	}
	if len(readErrors) > 0 {
		return nil, fmt.Errorf("read loaded Codex threads: %s", strings.Join(readErrors, "; "))
	}
	var all []surface.Session
	params := codexThreadListParams(codexSweepPageLimit)
	for {
		page, next, err := c.listPageCursor(ctx, conn, params, runtimes)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		params["cursor"] = next
	}
	return mergeCodexSessions(loaded, all), nil
}

func (c *Codex) listLoaded(ctx context.Context, conn codexClient, limit int, runtimes *codexRuntimes) ([]surface.Session, error) {
	sessions, readErrors, err := c.readLoaded(ctx, conn, limit, runtimes)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 && len(readErrors) > 0 {
		return nil, fmt.Errorf("read loaded Codex threads: %s", strings.Join(readErrors, "; "))
	}
	return sessions, nil
}

func (c *Codex) readLoaded(ctx context.Context, conn codexClient, limit int, runtimes *codexRuntimes) ([]surface.Session, []string, error) {
	ids, err := c.loadedThreadIDs(ctx, conn, limit)
	if err != nil {
		return nil, nil, err
	}
	sessions := make([]surface.Session, 0, len(ids))
	var readErrors []string
	for _, id := range ids {
		thread, err := c.readThreadObject(ctx, conn, id)
		if err != nil {
			readErrors = append(readErrors, err.Error())
			continue
		}
		if codexListsThread(thread) {
			sessions = append(sessions, runtimes.session(thread))
		}
	}
	return sessions, readErrors, nil
}

func (c *Codex) loadedThreadIDs(ctx context.Context, conn codexClient, limit int) ([]string, error) {
	params := map[string]any{}
	if limit > 0 {
		params["limit"] = limit
	}
	resp, err := conn.Request(ctx, "thread/loaded/list", params, 5*time.Second)
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

func (c *Codex) listRecent(ctx context.Context, conn codexClient, runtimes *codexRuntimes) ([]surface.Session, error) {
	return c.listPage(ctx, conn, codexThreadListParams(codexRecentListLimit), runtimes)
}

// codexThreadListParams asks for every conversation a person or Agenthail can
// address. thread/list defaults to interactive sources and filters by model
// provider unless told otherwise, which would hide spawned subagents,
// app-server threads and threads under other providers. Exec runs and Codex's
// own review, compaction and memory subagents stay out.
func codexThreadListParams(limit int) map[string]any {
	return map[string]any{
		"limit":          limit,
		"sortKey":        "recency_at",
		"sortDirection":  "desc",
		"useStateDbOnly": true,
		"sourceKinds":    []string{"cli", "vscode", "appServer", "subAgentThreadSpawn"},
		"modelProviders": []string{},
	}
}

func (c *Codex) listPage(ctx context.Context, conn codexClient, params map[string]any, runtimes *codexRuntimes) ([]surface.Session, error) {
	sessions, _, err := c.listPageCursor(ctx, conn, params, runtimes)
	return sessions, err
}

func (c *Codex) listPageCursor(ctx context.Context, conn codexClient, params map[string]any, runtimes *codexRuntimes) ([]surface.Session, string, error) {
	resp, err := conn.Request(ctx, "thread/list", params, 10*time.Second)
	if err != nil {
		return nil, "", err
	}
	result, _ := resp["result"].(map[string]any)
	threads, _ := result["data"].([]any)
	sessions := make([]surface.Session, 0, len(threads))
	for _, value := range threads {
		thread, _ := value.(map[string]any)
		if codexListsThread(thread) {
			sessions = append(sessions, runtimes.session(thread))
		}
	}
	return sessions, str(result, "nextCursor"), nil
}

// codexListsThread excludes threads that are not conversations to address:
// ephemeral threads, which Codex never stores, and subagents Codex runs for
// itself (review, compaction, memory) with no parent thread to sit under.
func codexListsThread(thread map[string]any) bool {
	if ephemeral, _ := thread["ephemeral"].(bool); ephemeral {
		return false
	}
	source, _ := thread["source"].(map[string]any)
	if _, subagent := source["subAgent"]; subagent && codexSubagent(thread) == nil {
		return false
	}
	return true
}

func codexSession(thread map[string]any) surface.Session {
	session := surface.Session{
		ID:         str(thread, "id"),
		Surface:    surface.KindCodex,
		Name:       surface.DeriveName(str(thread, "name"), str(thread, "preview"), 60),
		Cwd:        str(thread, "cwd"),
		Status:     codexStatus(thread["status"]),
		Source:     codexSource(thread["source"]),
		WaitingOn:  codexSessionWaiting(str(thread, "id"), thread["status"]),
		Subagent:   codexSubagent(thread),
		Transcript: str(thread, "path"),
	}
	setCodexTransport(&session, "")
	if timestamp, ok := thread["recencyAt"].(float64); ok && timestamp > 0 {
		session.LastActive = time.Unix(int64(timestamp), 0)
	}
	return session
}

// codexWaitingKind reads the waiting flags of an active ThreadStatus: the
// app-server sets them while an approval or user-input request is pending.
func codexWaitingKind(status any) surface.WaitingKind {
	object, _ := status.(map[string]any)
	if object == nil || object["type"] != "active" {
		return ""
	}
	flags, _ := object["activeFlags"].([]any)
	for _, flag := range flags {
		switch flag {
		case "waitingOnApproval":
			return surface.WaitingApproval
		case "waitingOnUserInput":
			return surface.WaitingInput
		}
	}
	return ""
}

func codexSessionWaiting(threadID string, status any) *surface.Waiting {
	return codexWaiting(threadID, codexWaitingKind(status))
}

// codexWaiting names what a thread waits on the same way from a thread
// listing and from an observation, so either one recognizes the other's
// record. Codex reports the wait on the thread, without the request id.
func codexWaiting(threadID string, kind surface.WaitingKind) *surface.Waiting {
	if kind == "" {
		return nil
	}
	return &surface.Waiting{Kind: kind, ID: threadID + ":" + string(kind)}
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
	session, err := c.resolve(ctx, target)
	if session != nil {
		applyCodexPendingModel(session)
	}
	return session, err
}

func (c *Codex) resolve(ctx context.Context, target string) (*surface.Session, error) {
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
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	reader := runtimes.reader(id)
	if reader == nil {
		return nil, runtimes.unavailable()
	}
	thread, err := c.readThreadObject(ctx, reader, id)
	if err != nil {
		return nil, err
	}
	session := runtimes.session(thread)
	if session.ID == "" {
		session.ID = id
	}
	return &session, nil
}

func (c *Codex) readThreadObject(ctx context.Context, conn codexClient, id string) (map[string]any, error) {
	response, err := conn.Request(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("thread/read: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	if thread == nil {
		return nil, fmt.Errorf("thread/read response missing thread")
	}
	return thread, nil
}

func (c *Codex) SearchSessions(ctx context.Context, query string, limit int) ([]surface.SessionSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("Codex history search requires a query")
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	clients := runtimes.readers()
	if len(clients) == 0 {
		return nil, runtimes.unavailable()
	}
	params := map[string]any{
		"searchTerm":    query,
		"limit":         limit,
		"sortKey":       "recency_at",
		"sortDirection": "desc",
		"archived":      false,
	}
	var response map[string]any
	var err error
	for _, client := range clients {
		if response, err = client.Request(ctx, "thread/search", params, 5*time.Second); err == nil {
			break
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
		session := runtimes.session(thread)
		if session.ID == "" {
			continue
		}
		output = append(output, surface.SessionSearchResult{Session: session, Snippet: str(entry, "snippet")})
	}
	return output, nil
}

func (c *Codex) Observe(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	conn, err := c.openReader(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	thread, err := c.readObservationThread(ctx, conn, sess.ID)
	if err != nil {
		return nil, err
	}
	observation := codexObservation(thread)
	if observation.Status == surface.StatusBusy {
		// Only thread status carries the pending approval and input flags.
		response, err := conn.Request(ctx, "thread/read", map[string]any{"threadId": sess.ID, "includeTurns": false}, 5*time.Second)
		if err != nil {
			return nil, fmt.Errorf("thread/read: %w", err)
		}
		result, _ := response["result"].(map[string]any)
		value, _ := result["thread"].(map[string]any)
		thread.Waiting = codexWaitingKind(value["status"])
		observation = codexObservation(thread)
	}
	return observation, nil
}

// activeTurnID returns the thread's running turn, or "" when none runs. A new
// thread whose turns cannot be listed yet has none, so its first turn can start.
func (c *Codex) activeTurnID(ctx context.Context, conn codexClient, threadID string) (string, error) {
	response, err := conn.Request(ctx, "thread/turns/list", map[string]any{
		"threadId":      threadID,
		"limit":         1,
		"sortDirection": "desc",
	}, 5*time.Second)
	if codexTurnsNotListedYet(err) {
		return "", nil
	}
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

// StartSession creates the thread on Codex Desktop when its bridge is
// reachable and on Codex's app-server daemon otherwise.
func (c *Codex) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return nil, nil, surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	if client, desktopErr := c.openDesktop(ctx); desktopErr == nil {
		defer client.Close()
		return c.startSessionOnTransport(ctx, client, options, codexTransportDesktop)
	} else if client, managedErr := c.openManaged(ctx); managedErr == nil {
		defer client.Close()
		return c.startSessionOnTransport(ctx, client, options, codexTransportManaged)
	} else {
		return nil, nil, surface.DeliveryUnavailable(fmt.Errorf("no Codex runtime can create the conversation: %v; %v", desktopErr, managedErr))
	}
}

// PrepareManagedTerminalSession creates the provider-owned thread that the
// interactive terminal will resume. It does not start a model turn.
func (c *Codex) PrepareManagedTerminalSession(ctx context.Context, cwd, model string) (*surface.Session, error) {
	if !c.managed {
		return nil, errors.New("managed Codex terminal preparation requires the managed runtime")
	}
	client, err := c.openManaged(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return prepareManagedTerminalSession(ctx, client, cwd, model)
}

func prepareManagedTerminalSession(ctx context.Context, client codexClient, cwd, model string) (*surface.Session, error) {
	params := map[string]any{"threadSource": "agenthail-terminal", "serviceName": "agenthail"}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if model != "" {
		params["model"] = model
	}
	response, err := client.Request(ctx, "thread/start", params, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("thread/start managed terminal: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	threadID := str(thread, "id")
	if threadID == "" {
		return nil, errors.New("thread/start managed terminal returned no thread id")
	}
	session := codexSession(thread)
	session.ID = threadID
	session.Surface = surface.KindCodex
	setCodexTransport(&session, codexTransportManaged)
	session.Source = "agenthail"
	if session.Cwd == "" {
		session.Cwd = str(result, "cwd")
	}
	if session.Cwd == "" {
		session.Cwd = cwd
	}
	return &session, nil
}

func (c *Codex) startSessionOnTransport(ctx context.Context, client codexClient, options surface.SessionStartOptions, transport string) (*surface.Session, *surface.SendResult, error) {
	if err := options.TurnOptions.Validate(surface.KindCodex); err != nil {
		return nil, nil, err
	}
	if options.Worktree != "" || options.Agent != "" || options.PermissionMode != "" || options.Name != "" || options.ConfigRoot != "" {
		return nil, nil, fmt.Errorf("name, worktree, agent, permission-mode and config root creation options require Claude")
	}
	message := strings.TrimSpace(options.Message)
	if message == "" {
		return nil, nil, fmt.Errorf("message is required")
	}
	if err := refuseCodexComposerCommand(options.Message); err != nil {
		return nil, nil, err
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
		LastActive: time.Now(),
	}
	setCodexTransport(session, transport)
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
	if err := refuseCodexComposerCommand(message); err != nil {
		return nil, err
	}
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	conn, err := c.openWriter(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	active, err := c.activeTurnID(ctx, conn, sess.ID)
	if err != nil {
		return nil, surface.DeliveryUnavailable(fmt.Errorf("inspect active turn: %w", err))
	}
	if active != "" {
		return &surface.SendResult{UUID: sess.ID, Accepted: false}, nil
	}
	params := map[string]any{
		"threadId": sess.ID,
		"input":    codexUserInput(message, options.Images),
	}
	model, notice := options.Model, ""
	_, pending := loadCodexPendingModel(sess.ID)
	if pending && model == "" {
		current, err := codexThreadModel(ctx, conn, sess.ID)
		if err != nil {
			return nil, surface.DeliveryUnavailable(fmt.Errorf("read the model the pending choice replaces: %w", err))
		}
		model, notice = codexPendingModelForTurn(sess.ID, current)
	}
	if err := applyCodexTurnOptions(ctx, conn, sess, model, options.TurnOptions, params); err != nil {
		return nil, surface.DeliveryUnavailable(err)
	}
	resp, err := conn.Request(ctx, "turn/start", params, 10*time.Second)
	if err != nil {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("turn/start: %w", err))
	}
	if pending {
		if err := clearCodexPendingModel(sess.ID); err != nil {
			notice = strings.TrimSpace(notice + " The pending model choice could not be cleared: " + err.Error())
		}
	}
	turnID := sess.ID
	if result, _ := resp["result"].(map[string]any); result != nil {
		if turn, _ := result["turn"].(map[string]any); turn != nil && str(turn, "id") != "" {
			turnID = str(turn, "id")
		} else if value := str(result, "turnId"); value != "" {
			turnID = value
		}
	}
	return &surface.SendResult{UUID: turnID, Accepted: true, Notice: notice}, nil
}

// NativeImages is true on every route: each Codex transport speaks the
// app-server protocol, whose turn input takes local image paths.
func (c *Codex) NativeImages(*surface.Session, surface.Route) bool { return true }

// refuseCodexComposerCommand refuses a message that reads as a Codex slash
// command. Codex runs those only in its own composer; turn input passes the
// text to the model as a prompt, so the command never runs. As in that
// composer, text that starts with a space or whose first word holds another
// slash, such as a path, is a prompt.
func refuseCodexComposerCommand(message string) error {
	rest, ok := strings.CutPrefix(message, "/")
	if !ok {
		return nil
	}
	name := rest
	if end := strings.IndexFunc(rest, unicode.IsSpace); end >= 0 {
		name = rest[:end]
	}
	if name == "" || strings.Contains(name, "/") {
		return nil
	}
	return surface.DeliveryTerminal(fmt.Errorf("Codex runs /%s only in its own composer; sent here it would reach the model as plain text and nothing would run. To use a skill named %s, send $%s", name, name, name), surface.DeliveryInvalidRequest)
}

func codexUserInput(message string, images surface.Uploads) []map[string]any {
	input := make([]map[string]any, 0, 1+len(images))
	if message != "" || len(images) == 0 {
		input = append(input, map[string]any{"type": "text", "text": message})
	}
	for _, image := range images {
		input = append(input, map[string]any{"type": "localImage", "path": image.Path})
	}
	return input
}

func codexDirectInputAccepted(response map[string]any) bool {
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	accepts, _ := thread["canAcceptDirectInput"].(bool)
	return accepts
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
	if !sess.TranscriptOffsetSet {
		read, err := c.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: timelineItemLimit})
		if err != nil {
			return err
		}
		if read == nil || !read.TranscriptOffsetSet {
			return fmt.Errorf("Codex local transcript is unavailable: %w", surface.ErrTranscriptUnavailable)
		}
		sess.TranscriptOffset = read.TranscriptOffset
		sess.TranscriptOffsetSet = true
		sess.CodexPendingEventUser = read.CodexPendingEventUser
		sess.CodexPendingEventTurn = read.CodexPendingEventTurn
		sess.CodexCurrentTurnID = read.CodexCurrentTurnID
	}
	return c.streamTranscript(ctx, sess, uuid, onEvent, timeout)
}

func codexDesktopStreamKey(turnID, kind, itemID string) string {
	kind = codexDesktopKeyKind(kind)
	if turnID == "" {
		return "renderer:" + kind + ":" + itemID
	}
	key := "codex:" + turnID + ":" + kind
	if itemID != "" {
		key += ":" + itemID
	}
	return key
}

func codexDesktopKeyKind(kind string) string {
	switch kind {
	case "text", "assistant":
		return "assistant"
	case "tool_use", "toolCall":
		return "toolCall"
	case "tool_result", "toolResult":
		return "toolResult"
	default:
		return kind
	}
}

func (c *Codex) streamManaged(ctx context.Context, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	client, err := c.openReader(ctx, sess)
	if err != nil {
		return err
	}
	defer client.Close()
	return c.streamManagedClient(ctx, client, sess, uuid, onEvent, timeout)
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

// managedNotificationsHandled, when set by a test, runs after a managed
// stream has handled the notifications it drained.
var managedNotificationsHandled func()

// codexNotifier is a client that delivers app-server notifications as they
// arrive rather than only alongside a response.
type codexNotifier interface {
	DrainNotifications() []codexEvent
	Notified() <-chan struct{}
}

// managedStreamRead reads what the stream follows: the newest turn, or the
// whole thread when the stream's turn is no longer the newest.
func (c *Codex) managedStreamRead(ctx context.Context, client codexClient, threadID, turnID string) (*codexThread, error) {
	thread, err := c.readLatestTurn(ctx, client, threadID)
	if err != nil {
		return nil, err
	}
	if turnID != "" && codexTurnByID(thread, turnID) == nil {
		return c.readThread(ctx, client, threadID)
	}
	return thread, nil
}

// subscribeManagedThread asks the app-server to send this connection the
// thread's item, turn and token notifications. It subscribes only to a loaded
// thread, since resuming one that is not loaded would load it.
func (c *Codex) subscribeManagedThread(ctx context.Context, client codexClient, threadID string) bool {
	loaded, err := c.loadedThreadIDs(ctx, client, 0)
	if err != nil || !slices.Contains(loaded, threadID) {
		return false
	}
	_, err = client.Request(ctx, "thread/resume", map[string]any{"threadId": threadID, "excludeTurns": true}, 5*time.Second)
	return err == nil
}

func (c *Codex) streamManagedClient(ctx context.Context, client codexClient, sess *surface.Session, uuid string, onEvent func(surface.StreamEvent), timeout time.Duration) error {
	emitted := ""
	baselineTurnID := ""
	finalEmitted := false
	var lastContext surface.ContextUsage
	var nextContextPoll time.Time
	notifier, _ := client.(codexNotifier)
	subscribed := notifier != nil && c.subscribeManagedThread(ctx, client, sess.ID)
	// handle forwards the thread's notifications and reports whether one may
	// have changed what a read returns.
	handle := func() bool {
		if notifier == nil {
			return false
		}
		changed := false
		for _, event := range notifier.DrainNotifications() {
			if !codexContainsID(event.Params, sess.ID) {
				continue
			}
			if !strings.HasSuffix(event.Method, "/delta") {
				changed = true
			}
			if event.Method == "thread/status/changed" && !subscribed {
				subscribed = c.subscribeManagedThread(ctx, client, sess.ID)
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
		if managedNotificationsHandled != nil {
			managedNotificationsHandled()
		}
		return changed
	}
	if uuid == "" {
		thread, readErr := c.readLatestTurn(ctx, client, sess.ID)
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
	var notified <-chan struct{}
	if notifier != nil {
		notified = notifier.Notified()
	}
	deadline := time.Now().Add(timeout)
	for first := true; first || time.Now().Before(deadline); first = false {
		thread, err := c.managedStreamRead(ctx, client, sess.ID, uuid)
		if err != nil {
			return err
		}
		// A change reported while reading may postdate the read.
		reread := handle()
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
			} else if hasAssistant && assistant.Text != emitted {
				emitted = assistant.Text
				key := "managed:" + turn.ID
				onEvent(surface.StreamEvent{ID: key + ":text", ProviderKey: key + ":text", Version: uint64(len(assistant.Text)), Operation: "upsert", TurnID: turn.ID, Kind: "text", Text: assistant.Text})
			}
			if turn.Done {
				key := "managed:" + turn.ID
				version := uint64(len(turn.Assistant))
				if hasAssistant {
					version = uint64(len(assistant.Text))
				}
				onEvent(surface.StreamEvent{ID: key + ":done", ProviderKey: key + ":done", Version: version, Operation: "phase", TurnID: turn.ID, Kind: "done", Turn: codexRolloutTurnMetadata(ctx, codexTranscriptPath(sess), turn.ID)})
				return nil
			}
		}
		if reread {
			continue
		}
		// Wait for a notification that may change the turn, or the poll
		// interval, which still covers anything the app-server does not send.
		timer := time.NewTimer(transcriptPollInterval)
	wait:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-notified:
				if handle() {
					break wait
				}
			case <-timer.C:
				break wait
			}
		}
		timer.Stop()
	}
	return fmt.Errorf("stream timed out after %s: %w", timeout, surface.ErrStreamWindow)
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
		client, err := c.openReader(ctx, sess)
		if err != nil {
			return "", err
		}
		defer client.Close()
		return codexThreadModel(ctx, client, sess.ID)
	}
	change, err := c.SetModel(ctx, sess, name)
	if err != nil {
		return "", err
	}
	return change.Text(), nil
}

func (c *Codex) SetModel(ctx context.Context, sess *surface.Session, name string) (surface.ModelChange, error) {
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return surface.ModelChange{}, surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	client, err := c.openRoute(ctx, sess)
	if err != nil {
		return surface.ModelChange{}, surface.DeliveryUnavailable(err)
	}
	defer client.Close()
	if err := c.loadForDirectInput(ctx, client, sess, name); err != nil {
		if surface.IsDeliveryTerminal(err) {
			return surface.ModelChange{}, err
		}
		return surface.ModelChange{}, surface.DeliveryUnavailable(err)
	}
	current, err := codexThreadModel(ctx, client, sess.ID)
	if err != nil {
		return surface.ModelChange{}, err
	}
	if current == name {
		if err := clearCodexPendingModel(sess.ID); err != nil {
			return surface.ModelChange{}, fmt.Errorf("clear the pending model: %w", err)
		}
		return surface.ModelChange{Model: current}, nil
	}
	if err := saveCodexPendingModel(sess.ID, codexPendingModel{Model: name, From: current, SetAt: time.Now().UTC()}); err != nil {
		return surface.ModelChange{}, fmt.Errorf("record the pending model: %w", err)
	}
	return surface.ModelChange{Model: current, PendingModel: name, Limit: "A turn started from Codex Desktop's composer or its own queue runs on the model Desktop shows."}, nil
}

func codexThreadModel(ctx context.Context, client codexClient, threadID string) (string, error) {
	response, err := client.Request(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, 5*time.Second)
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

func (c *Codex) Models(ctx context.Context) ([]surface.ModelOption, error) {
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	clients := runtimes.readers()
	if len(clients) == 0 {
		return nil, runtimes.unavailable()
	}
	return listCodexModels(ctx, clients[0])
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
	conn, err := c.openWriter(ctx, sess)
	if err != nil {
		return err
	}
	defer conn.Close()
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
	if err := refuseCodexComposerCommand(message); err != nil {
		return err
	}
	lock, err := acquireCodexWriteLock(ctx)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer releaseCodexWriteLock(lock)
	conn, err := c.openWriter(ctx, sess)
	if err != nil {
		return err
	}
	defer conn.Close()
	turnID, err := c.activeTurnID(ctx, conn, sess.ID)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	if turnID == "" {
		return surface.DeliveryUnavailable(fmt.Errorf("%w (use 'send' instead)", surface.ErrNothingToSteer))
	}
	if _, err := conn.Request(ctx, "turn/steer", map[string]any{
		"threadId":       sess.ID,
		"expectedTurnId": turnID,
		"input":          []map[string]any{{"type": "text", "text": message}},
	}, 5*time.Second); err != nil {
		return surface.DeliveryOutcomeUnknown(fmt.Errorf("turn/steer: %w", err))
	}
	return nil
}

var localHTTPClient = &http.Client{Timeout: 5 * time.Second}

func (c *Codex) Tail(ctx context.Context, sess *surface.Session, n int) ([]surface.Exchange, error) {
	read, err := c.ReadSession(ctx, sess, surface.SessionReadRequest{Limit: n})
	if err != nil {
		return nil, err
	}
	return read.Exchanges, nil
}

func (c *Codex) readSessionFromRPC(ctx context.Context, sess *surface.Session, n int, recorded map[string]*surface.TurnMetadata) (*surface.SessionReadResult, error) {
	conn, err := c.openReader(ctx, sess)
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
			metadata := recorded[turn.ID]
			if metadata == nil {
				metadata = codexRPCTurnMetadata(turn)
			}
			exchanges = append(exchanges, surface.Exchange{User: turn.User, Assistant: turn.Assistant, Source: "rpc", Turn: metadata})
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
