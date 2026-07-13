package daemon

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

//go:embed dashboard/index.html
var dashboardHTML []byte

//go:embed dashboard/app.js
var dashboardJS []byte

//go:embed dashboard/tokens.css
var dashboardCSS []byte

const (
	dashboardStateCacheTTL = 30 * time.Second
	dashboardRefreshBudget = 18 * time.Second
	dashboardCookieMaxAge  = 365 * 24 * 60 * 60
)

type dashboardServer struct {
	server  *http.Server
	listen  string
	token   string
	stateMu sync.Mutex
	stateAt time.Time
	state   dashboardState
}

type dashboardSurface struct {
	Name         string               `json:"name"`
	Connected    bool                 `json:"connected"`
	Error        string               `json:"error,omitempty"`
	Capabilities surface.Capabilities `json:"capabilities"`
}

type dashboardSession struct {
	ID             string                `json:"id"`
	Surface        surface.SurfaceKind   `json:"surface"`
	Name           string                `json:"name"`
	Alias          string                `json:"alias,omitempty"`
	Status         surface.SessionStatus `json:"status"`
	LastActive     time.Time             `json:"lastActive,omitempty"`
	QueueCount     int                   `json:"queueCount"`
	Open           bool                  `json:"open"`
	Current        bool                  `json:"current"`
	CurrentReason  string                `json:"currentReason,omitempty"`
	Capabilities   surface.Capabilities  `json:"capabilities"`
	ReadOnly       bool                  `json:"readOnly,omitempty"`
	ReadOnlyReason string                `json:"readOnlyReason,omitempty"`
}

type dashboardState struct {
	UpdatedAt        time.Time          `json:"updatedAt"`
	Daemon           map[string]any     `json:"daemon"`
	Surfaces         []dashboardSurface `json:"surfaces"`
	Sessions         []dashboardSession `json:"sessions"`
	TotalSessions    int                `json:"totalSessions"`
	Queue            []dashboardQueue   `json:"queue"`
	Channels         []dashboardChannel `json:"channels"`
	Relays           []dashboardRelay   `json:"relays"`
	History          []dashboardHistory `json:"history"`
	CodexRecentHours int                `json:"codexRecentHours"`
}

type dashboardQueue struct {
	ID        int64  `json:"id"`
	SessionID string `json:"sessionId"`
	Target    string `json:"target"`
	Message   string `json:"message"`
	Model     string `json:"model,omitempty"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"lastError,omitempty"`
	QueuedAt  string `json:"queuedAt"`
}

type dashboardChannel struct {
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type dashboardRelay struct {
	ID      int64  `json:"id"`
	From    string `json:"from"`
	To      string `json:"to"`
	Pattern string `json:"pattern"`
}

type dashboardHistory struct {
	ID              int64  `json:"id"`
	CreatedAt       string `json:"createdAt"`
	Kind            string `json:"kind"`
	SessionID       string `json:"sessionId,omitempty"`
	SourceSessionID string `json:"sourceSessionId,omitempty"`
	Target          string `json:"target,omitempty"`
	Source          string `json:"source,omitempty"`
	QueueID         int64  `json:"queueId,omitempty"`
	Message         string `json:"message,omitempty"`
	Result          string `json:"result,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (d *Daemon) startDashboard() (*dashboardServer, error) {
	config, err := LoadDashboardConfig()
	if err != nil || !config.Enabled {
		return nil, err
	}
	token, err := dashboardToken()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", config.Listen)
	if err != nil {
		return nil, fmt.Errorf("listen dashboard on %s: %w", config.Listen, err)
	}
	dashboard := &dashboardServer{listen: config.Listen, token: token}
	dashboard.server = &http.Server{Handler: d.dashboardHandler(dashboard), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if serveErr := dashboard.server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			d.log.Printf("dashboard server: %s", serveErr)
		}
	}()
	d.log.Printf("dashboard enabled on http://%s", config.Listen)
	return dashboard, nil
}

func (d *Daemon) dashboardHandler(dashboard *dashboardServer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", dashboard.page)
	mux.HandleFunc("/app.js", dashboard.asset("application/javascript; charset=utf-8", dashboardJS))
	mux.HandleFunc("/tokens.css", dashboard.asset("text/css; charset=utf-8", dashboardCSS))
	mux.HandleFunc("/api/state", dashboard.guard(func(w http.ResponseWriter, r *http.Request) { d.dashboardStateCached(dashboard, w, r) }))
	mux.HandleFunc("/api/session", dashboard.guard(d.dashboardSessionHandler))
	mux.HandleFunc("/api/history", dashboard.guard(d.dashboardHistoryHandler))
	mux.HandleFunc("/api/action", dashboard.guard(d.dashboardActionHandler))
	mux.HandleFunc("/api/settings", dashboard.guard(d.dashboardSettingsHandler))
	mux.HandleFunc("/api/settings/remote-qr", dashboard.guard(d.dashboardRemoteQRHandler))
	return d.dashboardHeaders(mux)
}

func (d *Daemon) dashboardSettingsHandler(w http.ResponseWriter, r *http.Request) {
	config, err := LoadDashboardConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	notifications := GetNotificationStatus()
	if r.Method == http.MethodGet {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"dashboard": config, "notifications": notifications, "remoteAccess": RemoteAccessStatusForConfig(config), "daemon": map[string]any{"pid": os.Getpid(), "running": true}})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Action               string `json:"action"`
		CodexRecentHours     int    `json:"codexRecentHours"`
		NotificationsEnabled bool   `json:"notificationsEnabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&request); err != nil {
		http.Error(w, "invalid settings request", http.StatusBadRequest)
		return
	}
	switch request.Action {
	case "remote-enable":
		config, _, err = EnableRemoteAccess(config)
	case "remote-disable":
		config, err = DisableRemoteAccess(config)
	case "dashboard-config":
		config.CodexRecentHours = request.CodexRecentHours
		err = SaveDashboardConfig(config)
	case "notifications":
		if request.NotificationsEnabled {
			notifications, err = EnableNotifications()
		} else {
			err = DisableNotifications()
			notifications = GetNotificationStatus()
		}
	case "notification-settings":
		err = OpenNotificationSettings()
	default:
		http.Error(w, "unsupported settings action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "notifications": notifications})
}

func (d *Daemon) dashboardRemoteQRHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	config, err := LoadDashboardConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	status := RemoteAccessStatusForConfig(config)
	if !status.Enabled || status.URL == "" {
		http.Error(w, "remote access is not enabled", http.StatusConflict)
		return
	}
	image, err := RemoteAccessQR(status.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(image)
}

func (d *Daemon) dashboardHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 25
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			http.Error(w, "limit must be between 1 and 100", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	var beforeID int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			http.Error(w, "before must be a positive history id", http.StatusBadRequest)
			return
		}
		beforeID = parsed
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	queryText := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(kind) > 100 || len(queryText) > 200 {
		http.Error(w, "history filters are too long", http.StatusBadRequest)
		return
	}
	entries, hasMore, err := d.Registry.ListHistoryPage(limit, beforeID, kind, queryText)
	if err != nil {
		http.Error(w, fmt.Sprintf("read delivery history: %s", err), http.StatusInternalServerError)
		return
	}
	kinds, err := d.Registry.ListHistoryKinds()
	if err != nil {
		http.Error(w, fmt.Sprintf("read delivery history kinds: %s", err), http.StatusInternalServerError)
		return
	}
	items := make([]dashboardHistory, 0, len(entries))
	for _, entry := range entries {
		items = append(items, d.dashboardHistoryEntry(entry))
	}
	nextBefore := int64(0)
	if hasMore && len(entries) > 0 {
		nextBefore = entries[len(entries)-1].ID
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items, "hasMore": hasMore, "nextBefore": nextBefore, "kinds": kinds})
}

func (d *Daemon) dashboardHistoryEntry(entry registry.HistoryEntry) dashboardHistory {
	return dashboardHistory{ID: entry.ID, CreatedAt: entry.CreatedAt, Kind: entry.Kind, SessionID: entry.SessionID, SourceSessionID: entry.SourceSessionID, Target: d.resolveDisplay(entry.SessionID), Source: d.resolveDisplay(entry.SourceSessionID), QueueID: entry.QueueID, Message: entry.Message, Result: entry.Result, Error: entry.Error}
}

func (d *Daemon) dashboardStateCached(dashboard *dashboardServer, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	dashboard.stateMu.Lock()
	defer dashboard.stateMu.Unlock()
	if r.URL.Query().Get("fresh") != "1" && !dashboard.stateAt.IsZero() && time.Since(dashboard.stateAt) < dashboardStateCacheTTL {
		writeDashboardJSON(w, http.StatusOK, dashboard.state)
		return
	}
	refreshCtx, cancel := context.WithTimeout(r.Context(), dashboardRefreshBudget)
	defer cancel()
	state, err := d.dashboardState(refreshCtx)
	if err != nil {
		if !dashboard.stateAt.IsZero() {
			stale := dashboard.state
			stale.Daemon = cloneDashboardDaemon(stale.Daemon)
			stale.Daemon["stale"] = true
			stale.Daemon["refreshError"] = err.Error()
			writeDashboardJSON(w, http.StatusOK, stale)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dashboard.state = state
	dashboard.stateAt = time.Now()
	writeDashboardJSON(w, http.StatusOK, state)
}

func cloneDashboardDaemon(input map[string]any) map[string]any {
	output := make(map[string]any, len(input)+2)
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (dashboard *dashboardServer) asset(contentType string, body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

func (dashboard *dashboardServer) page(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if supplied := r.URL.Query().Get("token"); supplied != "" && subtle.ConstantTimeCompare([]byte(supplied), []byte(dashboard.token)) == 1 {
		http.SetCookie(w, &http.Cookie{Name: "agenthail_dashboard", Value: dashboard.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: dashboardCookieMaxAge})
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if !dashboard.authorized(r) {
		http.Error(w, "dashboard access token required; run 'agenthail dashboard'", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(dashboardHTML)
}

func (dashboard *dashboardServer) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !dashboard.authorized(r) {
			http.Error(w, "dashboard access token required", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			http.Error(w, "cross-origin dashboard request rejected", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (dashboard *dashboardServer) authorized(r *http.Request) bool {
	cookie, err := r.Cookie("agenthail_dashboard")
	return err == nil && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(dashboard.token)) == 1
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host
}

func (d *Daemon) dashboardHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (d *Daemon) dashboardStateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state, err := d.dashboardState(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeDashboardJSON(w, http.StatusOK, state)
}

func (d *Daemon) dashboardState(ctx context.Context) (dashboardState, error) {
	config, err := LoadDashboardConfig()
	if err != nil {
		return dashboardState{}, fmt.Errorf("load dashboard config: %w", err)
	}
	now := time.Now()
	counts, err := d.Registry.QueueCounts()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read queue counts: %w", err)
	}
	queue, err := d.Registry.ListQueue(false)
	if err != nil {
		return dashboardState{}, fmt.Errorf("read queue: %w", err)
	}
	channels, err := d.Registry.ListChannels()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read channels: %w", err)
	}
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read relays: %w", err)
	}
	history, err := d.Registry.ListHistory(50, "")
	if err != nil {
		return dashboardState{}, fmt.Errorf("read delivery history: %w", err)
	}
	state := dashboardState{UpdatedAt: now.UTC(), Daemon: map[string]any{"running": true, "pid": os.Getpid()}, Surfaces: make([]dashboardSurface, 0, len(d.Surfaces)), Queue: make([]dashboardQueue, 0, len(queue)), Channels: make([]dashboardChannel, 0, len(channels)), Relays: make([]dashboardRelay, 0, len(routes)), History: make([]dashboardHistory, 0, len(history)), CodexRecentHours: config.CodexRecentHours}
	for _, item := range queue {
		state.Queue = append(state.Queue, dashboardQueue{ID: item.ID, SessionID: item.SessionID, Target: d.resolveDisplay(item.SessionID), Message: item.Message, Model: item.Model, Status: item.Status, Attempts: item.Attempts, LastError: item.LastError, QueuedAt: item.QueuedAt})
	}
	for _, channel := range channels {
		members := make([]string, 0, len(channel.Members))
		for _, member := range channel.Members {
			members = append(members, d.resolveDisplay(member))
		}
		state.Channels = append(state.Channels, dashboardChannel{Name: channel.Name, Members: members})
	}
	for _, route := range routes {
		state.Relays = append(state.Relays, dashboardRelay{ID: route.ID, From: d.resolveDisplay(route.FromSession), To: d.resolveDisplay(route.ToSession), Pattern: route.Pattern})
	}
	for _, entry := range history {
		state.History = append(state.History, d.dashboardHistoryEntry(entry))
	}
	var mu sync.Mutex
	var wait sync.WaitGroup
	for _, adapter := range d.Surfaces {
		adapter := adapter
		wait.Add(1)
		go func() {
			defer wait.Done()
			listBudget := 12 * time.Second
			if deadline, ok := ctx.Deadline(); ok {
				remaining := time.Until(deadline)
				if remaining < listBudget {
					listBudget = remaining
				}
			}
			operationCtx, cancel := context.WithTimeout(ctx, listBudget)
			sessions, listErr := adapter.List(operationCtx)
			cancel()
			entry := dashboardSurface{Name: string(adapter.Name()), Connected: listErr == nil, Capabilities: adapter.Capabilities()}
			if listErr != nil {
				entry.Error = listErr.Error()
			}
			mu.Lock()
			state.Surfaces = append(state.Surfaces, entry)
			for _, session := range sessions {
				if registerErr := d.Registry.RegisterSession(session); registerErr != nil {
					continue
				}
				alias, _ := d.Registry.ReverseAlias(session.ID)
				open := session.Surface == surface.KindClaude && claudeProcessOpen(ctx, session.PID)
				current, reason := dashboardSessionPresence(session, counts[session.ID], open, config.CodexRecentHours, now)
				capabilities, readOnly, readOnlyReason := dashboardCapabilities(session, adapter.Capabilities())
				state.Sessions = append(state.Sessions, dashboardSession{ID: session.ID, Surface: session.Surface, Name: session.Name, Alias: alias, Status: session.Status, LastActive: session.LastActive, QueueCount: counts[session.ID], Open: open, Current: current, CurrentReason: reason, Capabilities: capabilities, ReadOnly: readOnly, ReadOnlyReason: readOnlyReason})
			}
			mu.Unlock()
		}()
	}
	wait.Wait()
	sort.Slice(state.Surfaces, func(i, j int) bool { return state.Surfaces[i].Name < state.Surfaces[j].Name })
	sort.Slice(state.Sessions, func(i, j int) bool {
		if state.Sessions[i].Status == surface.StatusBusy && state.Sessions[j].Status != surface.StatusBusy {
			return true
		}
		if state.Sessions[j].Status == surface.StatusBusy && state.Sessions[i].Status != surface.StatusBusy {
			return false
		}
		return state.Sessions[i].LastActive.After(state.Sessions[j].LastActive)
	})
	state.TotalSessions = len(state.Sessions)
	return state, nil
}

func dashboardCapabilities(session surface.Session, capabilities surface.Capabilities) (surface.Capabilities, bool, string) {
	if surface.IsReadOnlySession(&session) {
		return surface.Capabilities{}, true, surface.ReadOnlySessionReason(&session)
	}
	return capabilities, false, ""
}

func dashboardSessionPresence(session surface.Session, queueCount int, open bool, codexRecentHours int, now time.Time) (bool, string) {
	switch session.Surface {
	case surface.KindClaude:
		if queueCount > 0 {
			return true, "queued"
		}
		if !open {
			return false, ""
		}
		if session.Status == surface.StatusBusy {
			return true, "working"
		}
		return true, "open"
	case surface.KindCodex:
		if session.Status == surface.StatusBusy {
			return true, "working"
		}
		if queueCount > 0 {
			return true, "queued"
		}
		if session.Status == surface.SessionStatus("notLoaded") || session.LastActive.IsZero() {
			return false, ""
		}
		if now.Sub(session.LastActive) <= time.Duration(codexRecentHours)*time.Hour {
			return true, "recent"
		}
		return false, ""
	default:
		if session.Status == surface.StatusBusy {
			return true, "working"
		}
		if queueCount > 0 {
			return true, "queued"
		}
		if !session.LastActive.IsZero() && now.Sub(session.LastActive) <= 24*time.Hour {
			return true, "recent"
		}
		return false, ""
	}
}

func claudeProcessOpen(ctx context.Context, pid int) bool {
	if pid <= 0 {
		return false
	}
	processCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(processCtx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return false
	}
	return filepath.Base(strings.TrimSpace(string(output))) == "claude"
}

func (d *Daemon) dashboardActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var request struct {
		Action    string `json:"action"`
		SessionID string `json:"sessionId"`
		Message   string `json:"message"`
		Model     string `json:"model"`
		QueueID   int64  `json:"queueId"`
		Channel   string `json:"channel"`
		TargetID  string `json:"targetId"`
		FromID    string `json:"fromId"`
		ToID      string `json:"toId"`
		Pattern   string `json:"pattern"`
		RelayID   int64  `json:"relayId"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 140<<10)).Decode(&request); err != nil {
		http.Error(w, "invalid dashboard request", http.StatusBadRequest)
		return
	}
	if request.Action == "queue-retry" {
		if request.QueueID <= 0 {
			http.Error(w, "queueId is required", http.StatusBadRequest)
			return
		}
		item, itemErr := d.Registry.QueueItem(request.QueueID)
		if itemErr != nil {
			http.Error(w, itemErr.Error(), http.StatusBadRequest)
			return
		}
		target, targetErr := d.Registry.Session(item.SessionID)
		if targetErr != nil {
			http.Error(w, targetErr.Error(), http.StatusBadRequest)
			return
		}
		if surface.IsReadOnlySession(target) {
			http.Error(w, surface.ReadOnlySessionReason(target), http.StatusConflict)
			return
		}
		if err := d.Registry.RetryMessage(request.QueueID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "queue-cancel" {
		if request.QueueID <= 0 {
			http.Error(w, "queueId is required", http.StatusBadRequest)
			return
		}
		if err := d.Registry.CancelMessage(request.QueueID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "channel-create" {
		if strings.TrimSpace(request.Channel) == "" {
			http.Error(w, "channel name is required", http.StatusBadRequest)
			return
		}
		if _, err := d.Registry.CreateChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "channel-delete" {
		if err := d.Registry.DeleteChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "channel-send" {
		channel := strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")
		if channel == "" || strings.TrimSpace(request.Message) == "" {
			http.Error(w, "channel and message are required", http.StatusBadRequest)
			return
		}
		members, membersErr := d.Registry.ChannelMembers(channel)
		if membersErr != nil {
			http.Error(w, membersErr.Error(), http.StatusBadRequest)
			return
		}
		if len(members) == 0 {
			http.Error(w, "channel has no members", http.StatusBadRequest)
			return
		}
		operationCtx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
		defer cancel()
		sent, queued, failed := 0, 0, 0
		for _, member := range members {
			session, sessionErr := d.Registry.Session(member)
			if sessionErr != nil {
				failed++
				continue
			}
			adapter := d.surfaceForKind(session.Surface)
			if adapter == nil || surface.IsReadOnlySession(session) {
				failed++
				continue
			}
			receipt, deliverErr := (delivery.Dispatcher{Registry: d.Registry}).Deliver(operationCtx, adapter, session, request.Message, "")
			if deliverErr != nil {
				failed++
				continue
			}
			if receipt.Disposition == delivery.DispositionQueued {
				queued++
			} else {
				sent++
			}
		}
		if failed > 0 {
			http.Error(w, fmt.Sprintf("channel delivery: %d sent, %d queued, %d failed", sent, queued, failed), http.StatusConflict)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": sent, "queued": queued, "failed": failed})
		return
	}
	if request.Action == "channel-add" || request.Action == "channel-remove" {
		if request.TargetID == "" || strings.TrimSpace(request.Channel) == "" {
			http.Error(w, "channel and targetId are required", http.StatusBadRequest)
			return
		}
		targetID, resolveErr := d.Registry.ResolveTarget(request.TargetID)
		if resolveErr != nil {
			http.Error(w, fmt.Sprintf("resolve agent: %s", resolveErr), http.StatusBadRequest)
			return
		}
		var actionErr error
		if request.Action == "channel-add" {
			target, targetErr := d.Registry.Session(targetID)
			if targetErr != nil {
				http.Error(w, "session not found", http.StatusNotFound)
				return
			}
			if surface.IsReadOnlySession(target) {
				http.Error(w, surface.ReadOnlySessionReason(target), http.StatusConflict)
				return
			}
			actionErr = d.Registry.AddToChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#"), targetID)
		} else {
			actionErr = d.Registry.RemoveFromChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#"), targetID)
		}
		if actionErr != nil {
			http.Error(w, actionErr.Error(), http.StatusBadRequest)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "relay-add" {
		if request.FromID == "" || request.ToID == "" {
			http.Error(w, "fromId and toId are required", http.StatusBadRequest)
			return
		}
		fromID, fromErr := d.Registry.ResolveTarget(request.FromID)
		if fromErr != nil {
			http.Error(w, fmt.Sprintf("resolve source agent: %s", fromErr), http.StatusBadRequest)
			return
		}
		toID, toErr := d.Registry.ResolveTarget(request.ToID)
		if toErr != nil {
			http.Error(w, fmt.Sprintf("resolve destination agent: %s", toErr), http.StatusBadRequest)
			return
		}
		toSession, toSessionErr := d.Registry.Session(toID)
		if toSessionErr != nil {
			http.Error(w, "destination session not found", http.StatusNotFound)
			return
		}
		if surface.IsReadOnlySession(toSession) {
			http.Error(w, surface.ReadOnlySessionReason(toSession), http.StatusConflict)
			return
		}
		pattern := request.Pattern
		if pattern == "" {
			pattern = ".*"
		}
		if _, err := d.Registry.AddRoute(fromID, toID, pattern); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "relay-remove" {
		if err := d.Registry.RemoveRoute(request.RelayID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.SessionID == "" {
		http.Error(w, "sessionId is required", http.StatusBadRequest)
		return
	}
	session, err := d.Registry.Session(request.SessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	adapter := d.surfaceForKind(session.Surface)
	if adapter == nil {
		http.Error(w, "surface is not configured", http.StatusConflict)
		return
	}
	_, readOnly, reason := dashboardCapabilities(*session, adapter.Capabilities())
	if readOnly {
		http.Error(w, reason, http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	var result any
	switch request.Action {
	case "send":
		if strings.TrimSpace(request.Message) == "" {
			http.Error(w, "message is required", http.StatusBadRequest)
			return
		}
		receipt, actionErr := (delivery.Dispatcher{Registry: d.Registry}).DeliverWithOptions(ctx, adapter, session, request.Message, "", surface.SendOptions{Model: request.Model})
		if actionErr != nil {
			http.Error(w, actionErr.Error(), http.StatusBadGateway)
			return
		}
		result = receipt
	case "steer":
		if !adapter.Capabilities().Steer || strings.TrimSpace(request.Message) == "" {
			http.Error(w, "this session cannot be steered", http.StatusBadRequest)
			return
		}
		err = adapter.Steer(ctx, session, request.Message)
	case "interrupt":
		if !adapter.Capabilities().Interrupt {
			http.Error(w, "this session cannot be interrupted", http.StatusBadRequest)
			return
		}
		err = adapter.Interrupt(ctx, session)
	case "compact":
		if !adapter.Capabilities().Compact {
			http.Error(w, "this session cannot be compacted", http.StatusBadRequest)
			return
		}
		err = adapter.Compact(ctx, session)
	case "goal-set":
		if !adapter.Capabilities().Goal || strings.TrimSpace(request.Message) == "" {
			http.Error(w, "this session cannot accept a goal", http.StatusBadRequest)
			return
		}
		err = adapter.GoalSet(ctx, session, request.Message)
	case "goal-clear":
		if !adapter.Capabilities().Goal {
			http.Error(w, "this session does not support goals", http.StatusBadRequest)
			return
		}
		err = adapter.GoalClear(ctx, session)
	case "model":
		if !adapter.Capabilities().Model {
			http.Error(w, "this session does not support model switching", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(request.Model) == "" {
			http.Error(w, "model is required", http.StatusBadRequest)
			return
		}
		result, err = adapter.Model(ctx, session, request.Model)
	default:
		http.Error(w, "unsupported dashboard action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

func (d *Daemon) dashboardSessionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("id")
	if sessionID == "" {
		http.Error(w, "session id is required", http.StatusBadRequest)
		return
	}
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	adapter := d.surfaceForKind(session.Surface)
	if adapter == nil {
		http.Error(w, "surface is not configured", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	limit := 20
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if parsed, parseErr := strconv.Atoi(rawLimit); parseErr == nil && parsed >= 4 && parsed <= 40 {
			limit = parsed
		}
	}
	exchanges, err := adapter.Tail(ctx, session, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("load conversation: %s", err), http.StatusBadGateway)
		return
	}
	alias, _ := d.Registry.ReverseAlias(session.ID)
	capabilities, readOnly, readOnlyReason := dashboardCapabilities(*session, adapter.Capabilities())
	response := map[string]any{"session": session, "alias": alias, "exchanges": exchanges, "capabilities": capabilities, "readOnly": readOnly, "readOnlyReason": readOnlyReason}
	if capabilities.Goal {
		if goal, goalErr := adapter.GoalGet(ctx, session); goalErr == nil {
			response["goal"] = goal
		}
	}
	if capabilities.Model {
		if model, modelErr := adapter.Model(ctx, session, ""); modelErr == nil {
			response["model"] = model
		}
	}
	if lister, ok := adapter.(surface.ModelLister); ok && capabilities.Model {
		if models, modelsErr := lister.Models(ctx); modelsErr == nil {
			response["models"] = models
		}
	}
	writeDashboardJSON(w, http.StatusOK, response)
}

func writeDashboardJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
