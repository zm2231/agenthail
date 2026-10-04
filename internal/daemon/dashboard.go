package daemon

import (
	"context"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

//go:embed dashboard/index.html
var dashboardHTML []byte

//go:embed dashboard/app.js
var dashboardJS []byte

//go:embed dashboard/tokens.css
var dashboardCSS []byte

//go:embed dashboard/logo.png
var dashboardLogo []byte

//go:embed dashboard/favicon.png
var dashboardFavicon []byte

const (
	dashboardStateCacheTTL      = 30 * time.Second
	dashboardRefreshBudget      = 18 * time.Second
	dashboardCookieMaxAge       = 365 * 24 * 60 * 60
	sessionTranscriptJSONBudget = 512 << 10
	sessionTranscriptFieldLimit = 64 << 10
)

type dashboardServer struct {
	server        *http.Server
	cancel        context.CancelFunc
	listen        string
	token         string
	stateMu       sync.Mutex
	stateAt       time.Time
	stateVersion  atomic.Uint64
	cachedVersion uint64
	state         dashboardState
}

func (d *dashboardServer) invalidate() {
	d.stateVersion.Add(1)
}

type dashboardSurface struct {
	Name         string                 `json:"name"`
	Connected    bool                   `json:"connected"`
	Error        string                 `json:"error,omitempty"`
	Health       string                 `json:"health"`
	HealthDetail string                 `json:"healthDetail,omitempty"`
	Runtime      *surface.RuntimeStatus `json:"runtime,omitempty"`
	RepairAction string                 `json:"repairAction,omitempty"`
	RepairLabel  string                 `json:"repairLabel,omitempty"`
	Capabilities surface.Capabilities   `json:"capabilities"`
}

type dashboardSession struct {
	ID                string                     `json:"id"`
	Surface           surface.SurfaceKind        `json:"surface"`
	Name              string                     `json:"name"`
	Cwd               string                     `json:"cwd,omitempty"`
	Alias             string                     `json:"alias,omitempty"`
	Status            surface.SessionStatus      `json:"status"`
	LastActive        time.Time                  `json:"lastActive,omitempty"`
	QueueCount        int                        `json:"queueCount"`
	Open              bool                       `json:"open"`
	Current           bool                       `json:"current"`
	CurrentReason     string                     `json:"currentReason,omitempty"`
	Capabilities      surface.Capabilities       `json:"capabilities"`
	ReadOnly          bool                       `json:"readOnly,omitempty"`
	ReadOnlyReason    string                     `json:"readOnlyReason,omitempty"`
	Source            string                     `json:"source,omitempty"`
	Transport         string                     `json:"transport,omitempty"`
	HostProject       *catalogHostProject        `json:"hostProject,omitempty"`
	Checkout          *catalogCheckout           `json:"checkout,omitempty"`
	ObservedAt        time.Time                  `json:"observedAt,omitempty"`
	UnavailableReason string                     `json:"unavailableReason,omitempty"`
	Runtime           *surface.Runtime           `json:"runtime,omitempty"`
	Freshness         *registry.CatalogFreshness `json:"freshness,omitempty"`
}

type dashboardState struct {
	UpdatedAt          time.Time                  `json:"updatedAt"`
	EventCursor        uint64                     `json:"eventCursor"`
	HostEpoch          string                     `json:"hostEpoch"`
	CatalogSeq         uint64                     `json:"catalogSeq"`
	Daemon             map[string]any             `json:"daemon"`
	Surfaces           []dashboardSurface         `json:"surfaces"`
	Sessions           []dashboardSession         `json:"sessions"`
	TotalSessions      int                        `json:"totalSessions"`
	NextCursor         string                     `json:"nextCursor,omitempty"`
	Queue              []dashboardQueue           `json:"queue"`
	Channels           []dashboardChannel         `json:"channels"`
	Relays             []dashboardRelay           `json:"relays"`
	History            []dashboardHistory         `json:"history"`
	Attention          []dashboardAttention       `json:"attention"`
	DeliveryProblems   []registry.DeliveryProblem `json:"deliveryProblems"`
	CodexRecentHours   int                        `json:"codexRecentHours"`
	BusyDelivery       string                     `json:"busyDelivery"`
	catalogPageApplied bool
	catalogPageFilter  string
	catalogPageOffset  int
	catalogPageLimit   int
	catalogPageHasMore bool
}

type dashboardAttention struct {
	ID              int64  `json:"id"`
	SessionID       string `json:"sessionId"`
	Target          string `json:"target"`
	QueueID         int64  `json:"queueId"`
	Reason          string `json:"reason"`
	RequestedAction string `json:"requestedAction"`
	CreatedAt       string `json:"createdAt"`
}

type dashboardQueue struct {
	surface.TurnOptions
	SourceSessionID string                   `json:"sourceSessionId,omitempty"`
	ID              int64                    `json:"id"`
	SessionID       string                   `json:"sessionId"`
	Target          string                   `json:"target"`
	Message         string                   `json:"message"`
	Model           string                   `json:"model,omitempty"`
	Status          string                   `json:"status"`
	Attempts        int                      `json:"attempts"`
	LastError       string                   `json:"lastError,omitempty"`
	QueuedAt        string                   `json:"queuedAt"`
	ExpiresAt       int64                    `json:"expiresAt,omitempty"`
	Historical      bool                     `json:"historical"`
	Evidence        surface.DeliveryEvidence `json:"evidence"`
	Operation       registry.QueueOperation  `json:"operation"`
	BusyDelivery    string                   `json:"busyDelivery,omitempty"`
}

type dashboardChannel struct {
	Name          string                   `json:"name"`
	Members       []string                 `json:"members"`
	MemberDetails []dashboardChannelMember `json:"memberDetails"`
}

type dashboardChannelMember struct {
	ID      string `json:"id"`
	Display string `json:"display"`
}

type dashboardRelay struct {
	ID          int64  `json:"id"`
	From        string `json:"from"`
	To          string `json:"to"`
	Pattern     string `json:"pattern"`
	Once        bool   `json:"once"`
	Active      bool   `json:"active"`
	FireCount   int64  `json:"fireCount"`
	LastFiredAt string `json:"lastFiredAt,omitempty"`
}

type dashboardHistory struct {
	ID              int64                    `json:"id"`
	CreatedAt       string                   `json:"createdAt"`
	Kind            string                   `json:"kind"`
	SessionID       string                   `json:"sessionId,omitempty"`
	SourceSessionID string                   `json:"sourceSessionId,omitempty"`
	Target          string                   `json:"target,omitempty"`
	Source          string                   `json:"source,omitempty"`
	QueueID         int64                    `json:"queueId,omitempty"`
	Message         string                   `json:"message,omitempty"`
	Result          string                   `json:"result,omitempty"`
	Error           string                   `json:"error,omitempty"`
	Evidence        surface.DeliveryEvidence `json:"evidence,omitempty"`
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
	serverCtx, cancel := context.WithCancel(context.Background())
	dashboard := &dashboardServer{listen: config.Listen, token: token, cancel: cancel}
	dashboard.server = &http.Server{
		Handler:           d.dashboardHandler(dashboard),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
		BaseContext:       func(net.Listener) context.Context { return serverCtx },
	}
	go func() {
		if serveErr := dashboard.server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			d.log.Printf("dashboard server: %s", serveErr)
		}
	}()
	d.log.Printf("dashboard enabled on http://%s", config.Listen)
	return dashboard, nil
}

func (d *dashboardServer) shutdown() error {
	if d.cancel != nil {
		d.cancel()
	}
	return d.server.Close()
}

func (d *Daemon) dashboardHandler(dashboard *dashboardServer) http.Handler {
	d.dashboard = dashboard
	mux := http.NewServeMux()
	mux.HandleFunc("/", dashboard.page)
	mux.HandleFunc("/app.js", dashboard.asset("application/javascript; charset=utf-8", dashboardJS))
	mux.HandleFunc("/tokens.css", dashboard.asset("text/css; charset=utf-8", dashboardCSS))
	mux.HandleFunc("/logo.png", dashboard.asset("image/png", dashboardLogo))
	mux.HandleFunc("/favicon.png", dashboard.asset("image/png", dashboardFavicon))
	mux.HandleFunc("/favicon.ico", dashboard.asset("image/png", dashboardFavicon))
	mux.HandleFunc("/api/state", dashboard.guard(func(w http.ResponseWriter, r *http.Request) { d.dashboardStateCached(dashboard, w, r) }))
	mux.HandleFunc("/api/session", dashboard.guard(d.dashboardSessionHandler))
	mux.HandleFunc("/api/session-metadata", dashboard.guard(d.dashboardSessionMetadataHandler))
	mux.HandleFunc("/api/session-attachment", dashboard.guard(d.apiSessionAttachmentHandler))
	mux.HandleFunc("/api/session-stream", dashboard.guard(d.apiSessionStreamHandler))
	mux.HandleFunc("/api/models", dashboard.guard(d.dashboardModelsHandler))
	mux.HandleFunc("/api/search", dashboard.guard(d.dashboardSearchHandler))
	mux.HandleFunc("/api/history", dashboard.guard(d.dashboardHistoryHandler))
	mux.HandleFunc("/api/action", dashboard.guard(d.idempotentActionHandler(d.dashboardActionHandler)))
	mux.HandleFunc("/api/settings", dashboard.guard(d.dashboardSettingsHandler))
	mux.HandleFunc("/api/settings/remote-qr", dashboard.guard(d.dashboardRemoteQRHandler))
	d.registerAPIV1(mux, dashboard)
	return d.dashboardHeaders(mux)
}

func (d *Daemon) dashboardModelsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	adapter := d.surfaceForKind(surface.SurfaceKind(r.URL.Query().Get("surface")))
	lister, ok := adapter.(surface.ModelLister)
	if adapter == nil || !ok {
		http.Error(w, "this surface does not list models", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	models, err := lister.Models(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("list models: %s", err), http.StatusBadGateway)
		return
	}
	if models == nil {
		models = []surface.ModelOption{}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (d *Daemon) dashboardSettingsHandler(w http.ResponseWriter, r *http.Request) {
	config, err := LoadDashboardConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodGet {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"dashboard": config, "remoteAccess": RemoteAccessStatusForConfig(config), "notifications": GetNotificationStatus(), "daemon": map[string]any{"pid": os.Getpid(), "running": true}})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mutation := &mutationResponseWriter{ResponseWriter: w}
	w = mutation
	defer func() {
		if mutation.succeeded() {
			d.publishEvent("settings.updated", "", map[string]string{"source": "dashboard"})
		}
	}()
	var request struct {
		Action           string `json:"action"`
		CodexRecentHours int    `json:"codexRecentHours"`
		BusyDelivery     string `json:"busyDelivery"`
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
		if request.BusyDelivery != "" {
			config.BusyDelivery = deliverypolicy.Mode(request.BusyDelivery)
		}
		err = SaveDashboardConfig(config)
	case "notifications-enable":
		_, err = EnableNotifications()
	case "notifications-disable":
		err = DisableNotifications()
	case "notifications-settings":
		err = OpenNotificationSettings()
	case "notifications-test":
		if !GetNotificationStatus().Enabled {
			err = fmt.Errorf("desktop notifications are not enabled")
		} else {
			err = Notify("Agenthail", "Notifications are working")
		}
	default:
		http.Error(w, "unsupported settings action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
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
	return dashboardHistory{ID: entry.ID, CreatedAt: entry.CreatedAt, Kind: entry.Kind, SessionID: entry.SessionID, SourceSessionID: entry.SourceSessionID, Target: d.resolveDisplay(entry.SessionID), Source: d.resolveDisplay(entry.SourceSessionID), QueueID: entry.QueueID, Message: entry.Message, Result: entry.Result, Error: entry.Error, Evidence: entry.Evidence}
}

func (d *Daemon) dashboardStateCached(dashboard *dashboardServer, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, pageErr := catalogPageRequestFromHTTP(r)
	if pageErr != nil {
		code := "invalid_catalog_page"
		if strings.Contains(pageErr.Error(), "cursor") {
			code = "invalid_cursor"
		}
		writeAPIError(w, http.StatusBadRequest, code, pageErr.Error())
		return
	}
	hostEpoch, catalogSeq, catalogErr := d.Registry.CatalogState()
	dashboard.stateMu.Lock()
	version := dashboard.stateVersion.Load()
	if page == nil && catalogErr == nil && r.URL.Query().Get("fresh") != "1" && dashboard.cachedVersion == version && dashboard.state.HostEpoch == hostEpoch && dashboard.state.CatalogSeq == catalogSeq && !dashboard.stateAt.IsZero() && time.Since(dashboard.stateAt) < dashboardStateCacheTTL {
		state := dashboard.state
		dashboard.stateMu.Unlock()
		d.writeDashboardSnapshot(w, r, state)
		return
	}
	previous, hadPrevious := dashboard.state, !dashboard.stateAt.IsZero()
	dashboard.stateMu.Unlock()
	refreshCtx, cancel := context.WithTimeout(r.Context(), dashboardRefreshBudget)
	defer cancel()
	var state dashboardState
	var err error
	if page == nil {
		state, err = d.dashboardState(refreshCtx)
	} else {
		state, err = d.dashboardState(refreshCtx, *page)
	}
	if err != nil {
		if page != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if hadPrevious {
			stale := previous
			stale.Daemon = cloneDashboardDaemon(stale.Daemon)
			stale.Daemon["stale"] = true
			stale.Daemon["refreshError"] = err.Error()
			d.writeDashboardSnapshot(w, r, stale)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if page == nil {
		dashboard.stateMu.Lock()
		dashboard.state = state
		dashboard.stateAt = time.Now()
		dashboard.cachedVersion = version
		dashboard.stateMu.Unlock()
	}
	d.writeDashboardSnapshot(w, r, state)
}

func (d *Daemon) writeDashboardSnapshot(w http.ResponseWriter, r *http.Request, state dashboardState) {
	if state.catalogPageApplied {
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			decoded, err := base64.RawURLEncoding.DecodeString(raw)
			var cursor catalogPageCursor
			if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Epoch != state.HostEpoch || cursor.Seq != state.CatalogSeq || cursor.Filter != state.catalogPageFilter || cursor.Offset != state.catalogPageOffset || !cursor.UpdatedAt.Equal(state.UpdatedAt) {
				writeAPIError(w, http.StatusConflict, "catalog_changed", "Reload the catalog before continuing.")
				return
			}
		}
		if state.catalogPageHasMore {
			encoded, _ := json.Marshal(catalogPageCursor{Epoch: state.HostEpoch, Seq: state.CatalogSeq, UpdatedAt: state.UpdatedAt, Offset: state.catalogPageOffset + state.catalogPageLimit, Filter: state.catalogPageFilter})
			state.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
		}
		writeDashboardJSON(w, http.StatusOK, state)
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope != "" && scope != "all" && scope != "recent" && scope != "current" && scope != "running" {
		writeAPIError(w, http.StatusBadRequest, "invalid_scope", "Use all, recent, current, or running.")
		return
	}
	projectID, query := r.URL.Query().Get("projectId"), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	rows := make([]dashboardSession, 0, len(state.Sessions))
	for _, row := range state.Sessions {
		if (scope == "recent" || scope == "current") && !row.Current {
			continue
		}
		if scope == "running" && row.Status != surface.StatusBusy {
			continue
		}
		if projectID != "" && (row.HostProject == nil || row.HostProject.ID != projectID) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(row.Name+" "+row.Cwd+" "+row.Alias), query) {
			continue
		}
		rows = append(rows, row)
	}
	state.Sessions = rows
	state.TotalSessions = len(rows)
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > 200 {
			writeAPIError(w, http.StatusBadRequest, "invalid_limit", "Use a limit between 1 and 200.")
			return
		}
		filter := scope + "\x00" + projectID + "\x00" + query
		offset := 0
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			decoded, err := base64.RawURLEncoding.DecodeString(raw)
			var cursor catalogPageCursor
			if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Offset < 0 || cursor.Filter != filter {
				writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "The catalog page cursor is invalid.")
				return
			}
			if cursor.Epoch != state.HostEpoch || cursor.Seq != state.CatalogSeq {
				writeAPIError(w, http.StatusConflict, "catalog_changed", "Reload the catalog before continuing.")
				return
			}
			offset = cursor.Offset
		}
		if offset > len(rows) {
			writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "The catalog page cursor is invalid.")
			return
		}
		end := min(len(rows), offset+limit)
		state.Sessions = rows[offset:end]
		if end < len(rows) {
			encoded, _ := json.Marshal(catalogPageCursor{Epoch: state.HostEpoch, Seq: state.CatalogSeq, UpdatedAt: state.UpdatedAt, Offset: end, Filter: filter})
			state.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
		}
	} else if r.URL.Query().Get("cursor") != "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "A page cursor requires a limit.")
		return
	}
	writeDashboardJSON(w, http.StatusOK, state)
}

type catalogPageCursor struct {
	Epoch     string    `json:"epoch"`
	Seq       uint64    `json:"seq"`
	UpdatedAt time.Time `json:"updatedAt"`
	Offset    int       `json:"offset"`
	Filter    string    `json:"filter"`
}

func catalogPageRequestFromHTTP(r *http.Request) (*registry.CatalogPageRequest, error) {
	rawLimit := r.URL.Query().Get("limit")
	if rawLimit == "" {
		if r.URL.Query().Get("cursor") != "" {
			return nil, fmt.Errorf("a page cursor requires a limit")
		}
		return nil, nil
	}
	limit, err := strconv.Atoi(rawLimit)
	if err != nil || limit < 1 || limit > 200 {
		return nil, fmt.Errorf("use a limit between 1 and 200")
	}
	scope := r.URL.Query().Get("scope")
	if scope != "" && scope != "all" && scope != "recent" && scope != "current" && scope != "running" {
		return nil, fmt.Errorf("use all, recent, current, or running")
	}
	projectID, query := r.URL.Query().Get("projectId"), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	filter := scope + "\x00" + projectID + "\x00" + query
	request := &registry.CatalogPageRequest{Scope: scope, ProjectID: projectID, Query: query, Limit: limit, CodexRecentHours: 24}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		var cursor catalogPageCursor
		if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Offset < 0 || cursor.Filter != filter || cursor.UpdatedAt.IsZero() {
			return nil, fmt.Errorf("the catalog page cursor is invalid")
		}
		request.Offset = cursor.Offset
		request.Now = cursor.UpdatedAt
	} else {
		request.Now = time.Now().UTC()
	}
	return request, nil
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self' https: wss:; media-src blob:; base-uri 'none'; frame-ancestors 'none'")
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

func (d *Daemon) dashboardState(ctx context.Context, pageRequest ...registry.CatalogPageRequest) (dashboardState, error) {
	eventCursor := uint64(0)
	if d.events != nil {
		eventCursor = d.events.cursor()
	}
	config, err := LoadDashboardConfig()
	if err != nil {
		return dashboardState{}, fmt.Errorf("load dashboard config: %w", err)
	}
	var catalogSnapshot registry.CatalogSnapshot
	var page *registry.CatalogPage
	if len(pageRequest) > 0 {
		request := pageRequest[0]
		request.CodexRecentHours = config.CodexRecentHours
		loaded, err := d.Registry.CatalogSnapshotPage(request)
		if err != nil {
			return dashboardState{}, fmt.Errorf("read catalog page: %w", err)
		}
		catalogSnapshot = loaded.CatalogSnapshot
		page = &loaded
	} else {
		catalogSnapshot, err = d.Registry.CatalogSnapshot()
		if err != nil {
			return dashboardState{}, fmt.Errorf("read catalog state: %w", err)
		}
	}
	now := time.Now()
	counts, err := d.Registry.QueueCounts()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read queue counts: %w", err)
	}
	aliases, err := d.Registry.ListAliases()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read aliases: %w", err)
	}
	aliasByID := make(map[string]string, len(aliases))
	for _, alias := range aliases {
		aliasByID[alias.SessionID] = alias.Name
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
	attention, err := d.Registry.ListAttentionItems(false)
	if err != nil {
		return dashboardState{}, fmt.Errorf("read attention items: %w", err)
	}
	deliveryProblems, err := d.Registry.ListDeliveryProblems()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read delivery problems: %w", err)
	}
	state := dashboardState{UpdatedAt: now.UTC(), EventCursor: eventCursor, HostEpoch: catalogSnapshot.HostEpoch, CatalogSeq: catalogSnapshot.CatalogSeq, Daemon: map[string]any{"running": true, "pid": os.Getpid()}, Surfaces: make([]dashboardSurface, 0, len(d.Surfaces)), Queue: make([]dashboardQueue, 0, len(queue)), Channels: make([]dashboardChannel, 0, len(channels)), Relays: make([]dashboardRelay, 0, len(routes)), History: make([]dashboardHistory, 0, len(history)), Attention: make([]dashboardAttention, 0, len(attention)), DeliveryProblems: deliveryProblems, CodexRecentHours: config.CodexRecentHours, BusyDelivery: string(config.BusyDelivery)}
	if page != nil {
		state.catalogPageApplied = true
		state.catalogPageFilter = pageRequest[0].Scope + "\x00" + pageRequest[0].ProjectID + "\x00" + pageRequest[0].Query
		state.catalogPageOffset = page.Offset
		state.catalogPageLimit = page.Limit
		state.catalogPageHasMore = page.HasMore
		state.UpdatedAt = pageRequest[0].Now.UTC()
	}
	for _, item := range queue {
		state.Queue = append(state.Queue, dashboardQueue{TurnOptions: item.TurnOptions, ID: item.ID, SessionID: item.SessionID, SourceSessionID: item.SourceSessionID, Target: d.resolveDisplay(item.SessionID), Message: item.Message, Model: item.Model, Status: item.Status, Attempts: item.Attempts, LastError: item.LastError, QueuedAt: item.QueuedAt, ExpiresAt: item.ExpiresAt, Historical: item.Historical, Evidence: item.Evidence, Operation: item.Operation, BusyDelivery: item.BusyDelivery})
	}
	for _, channel := range channels {
		members := make([]string, 0, len(channel.Members))
		memberDetails := make([]dashboardChannelMember, 0, len(channel.Members))
		for _, member := range channel.Members {
			display := d.resolveDisplay(member)
			members = append(members, display)
			memberDetails = append(memberDetails, dashboardChannelMember{ID: member, Display: display})
		}
		state.Channels = append(state.Channels, dashboardChannel{Name: channel.Name, Members: members, MemberDetails: memberDetails})
	}
	for _, route := range routes {
		state.Relays = append(state.Relays, dashboardRelay{ID: route.ID, From: d.resolveDisplay(route.FromSession), To: d.resolveDisplay(route.ToSession), Pattern: route.Pattern, Once: route.Once, Active: route.Active, FireCount: route.FireCount, LastFiredAt: route.LastFiredAt})
	}
	for _, entry := range history {
		state.History = append(state.History, d.dashboardHistoryEntry(entry))
	}
	for _, item := range attention {
		state.Attention = append(state.Attention, dashboardAttention{ID: item.ID, SessionID: item.SessionID, Target: d.resolveDisplay(item.SessionID), QueueID: item.QueueID, Reason: item.Reason, RequestedAction: item.RequestedAction, CreatedAt: item.CreatedAt})
	}
	adapters := make(map[surface.SurfaceKind]surface.Surface, len(d.Surfaces))
	catalogSurface := make(map[surface.SurfaceKind]registry.CatalogSurfaceState, len(catalogSnapshot.Surfaces))
	for _, record := range catalogSnapshot.Surfaces {
		catalogSurface[record.Surface] = record
	}
	for _, adapter := range d.Surfaces {
		adapters[adapter.Name()] = adapter
		entry := dashboardSurface{Name: string(adapter.Name()), Connected: false, Health: "unknown", Capabilities: adapter.Capabilities()}
		if record, found := catalogSurface[adapter.Name()]; found {
			entry.Health = record.Health
			entry.HealthDetail = record.Detail
			entry.Connected = record.Health == "healthy"
		}
		state.Surfaces = append(state.Surfaces, entry)
	}
	catalogSessions := map[string]registry.CatalogSessionState{}
	sessions := make([]surface.Session, 0, len(catalogSnapshot.Sessions))
	for _, record := range catalogSnapshot.Sessions {
		catalogSessions[record.Session.ID] = record
		sessions = append(sessions, record.Session)
	}
	if page == nil && len(catalogSnapshot.Sessions) == 0 && len(catalogSnapshot.Surfaces) == 0 {
		var err error
		sessions, err = d.Registry.ListSessions(0)
		if err != nil {
			return dashboardState{}, fmt.Errorf("read session catalog: %w", err)
		}
	}
	for _, session := range sessions {
		adapter := adapters[session.Surface]
		if adapter == nil {
			continue
		}
		identity := catalogIdentity{}
		observedAt := time.Time{}
		if record, found := catalogSessions[session.ID]; found {
			_ = json.Unmarshal(record.HostProject, &identity.HostProject)
			_ = json.Unmarshal(record.Checkout, &identity.Checkout)
			identity.UnavailableReason = record.UnavailableReason
			observedAt = record.ObservedAt
		}
		if observedAt.IsZero() {
			observedAt = now.UTC()
		}
		entry := d.catalogSessionProjection(adapter, session, identity, observedAt, aliasByID[session.ID], counts[session.ID], false, config)
		if record, found := catalogSessions[session.ID]; found {
			entry.Freshness = &record.Freshness
		}
		if record, found := catalogSessions[session.ID]; found && record.ProjectionFingerprint != "" {
			var saved dashboardSession
			if json.Unmarshal([]byte(record.ProjectionFingerprint), &saved) == nil {
				entry.Open = saved.Open
			}
		}
		entry.QueueCount = counts[session.ID]
		entry.Current, entry.CurrentReason = dashboardSessionPresence(session, entry.QueueCount, entry.Open, config.CodexRecentHours, now)
		state.Sessions = append(state.Sessions, entry)
	}
	sort.Slice(state.Surfaces, func(i, j int) bool { return state.Surfaces[i].Name < state.Surfaces[j].Name })
	if page == nil {
		sort.Slice(state.Sessions, func(i, j int) bool {
			if state.Sessions[i].Status == surface.StatusBusy && state.Sessions[j].Status != surface.StatusBusy {
				return true
			}
			if state.Sessions[j].Status == surface.StatusBusy && state.Sessions[i].Status != surface.StatusBusy {
				return false
			}
			return state.Sessions[i].LastActive.After(state.Sessions[j].LastActive)
		})
	}
	if page != nil {
		state.TotalSessions = page.TotalMatching
	} else {
		state.TotalSessions = len(state.Sessions)
	}
	return state, nil
}

func (d *Daemon) dashboardSurfaceHealth(ctx context.Context, adapter surface.Surface, listErr error) dashboardSurface {
	entry := dashboardSurface{Name: string(adapter.Name()), Connected: listErr == nil, Health: "healthy", Capabilities: adapter.Capabilities()}
	if listErr != nil {
		entry.Error = listErr.Error()
		entry.Health = "unavailable"
		entry.HealthDetail = listErr.Error()
	}
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if checker, ok := adapter.(surface.HealthChecker); ok {
		if err := checker.Health(healthCtx); err != nil {
			if entry.Connected {
				entry.Health = "degraded"
			}
			entry.HealthDetail = err.Error()
			if adapter.Name() == surface.KindCodex {
				entry.RepairAction = "codex-launch"
				entry.RepairLabel = "Launch Codex through Agenthail"
			}
		}
	}
	if provider, ok := adapter.(surface.RuntimeStatusProvider); ok {
		runtimeStatus := provider.RuntimeStatus(healthCtx)
		if runtimeStatus.Name != "" {
			entry.Runtime = &runtimeStatus
			if !runtimeStatus.Reachable || !runtimeStatus.Durable {
				if entry.Health == "healthy" {
					entry.Health = "degraded"
				}
				if entry.HealthDetail == "" {
					entry.HealthDetail = runtimeStatus.Detail
				}
				if !runtimeStatus.Reachable && entry.RepairAction == "" {
					entry.RepairAction = "runtime-ensure"
					entry.RepairLabel = "Repair managed runtime"
				}
			}
		}
	}
	return entry
}

func (d *Daemon) ensureDashboardWritable(ctx context.Context, adapter surface.Surface, session *surface.Session) error {
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		return err
	}
	if err := d.Registry.RegisterSession(*session); err != nil {
		return err
	}
	return nil
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

func claudeOpenProcesses(ctx context.Context) map[int]bool {
	processCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(processCtx, "ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		return map[int]bool{}
	}
	return parseClaudeOpenProcesses(string(output))
}

func parseClaudeOpenProcesses(output string) map[int]bool {
	result := map[int]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err == nil && pid > 0 && filepath.Base(strings.Join(fields[1:], " ")) == "claude" {
			result[pid] = true
		}
	}
	return result
}

func (d *Daemon) dashboardActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mutation := &mutationResponseWriter{ResponseWriter: w}
	w = mutation
	defer func() {
		if mutation.succeeded() {
			d.publishEvent("state.changed", "", map[string]string{"source": "action"})
		}
	}()
	defer r.Body.Close()
	var request struct {
		surface.TurnOptions
		Name            string                     `json:"name"`
		Worktree        string                     `json:"worktree"`
		Agent           string                     `json:"agent"`
		PermissionMode  string                     `json:"permissionMode"`
		Fork            surface.ForkOptions        `json:"fork"`
		NativeQueue     surface.NativeQueueRequest `json:"nativeQueue"`
		Action          string                     `json:"action"`
		SourceSessionID string                     `json:"sourceSessionId"`
		SessionID       string                     `json:"sessionId"`
		Message         string                     `json:"message"`
		Alias           string                     `json:"alias"`
		Model           string                     `json:"model"`
		BusyDelivery    string                     `json:"busyDelivery"`
		QueueID         int64                      `json:"queueId"`
		Channel         string                     `json:"channel"`
		TargetID        string                     `json:"targetId"`
		FromID          string                     `json:"fromId"`
		ToID            string                     `json:"toId"`
		Pattern         string                     `json:"pattern"`
		Once            bool                       `json:"once"`
		RelayID         int64                      `json:"relayId"`
		DeliveryID      int64                      `json:"deliveryId"`
		Surface         string                     `json:"surface"`
		Cwd             string                     `json:"cwd"`
		Launcher        string                     `json:"launcher"`
		Approval        string                     `json:"approvalPolicy"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 140<<10)).Decode(&request); err != nil {
		http.Error(w, "invalid dashboard request", http.StatusBadRequest)
		return
	}
	if request.Action == "delivery-dismiss" {
		if request.DeliveryID <= 0 {
			http.Error(w, "deliveryId is required", http.StatusBadRequest)
			return
		}
		dismissed, err := d.Registry.DismissDeliveryProblem(request.DeliveryID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "delivery problem not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.catalog.flushCommitted(); err != nil {
			http.Error(w, fmt.Sprintf("publish delivery dismissal: %s", err), http.StatusInternalServerError)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "dismissed": dismissed})
		return
	}
	if request.Action == "session-create" {
		if request.Launcher != "" {
			if request.Worktree != "" || request.PermissionMode != "" || request.Approval != "" || !request.TurnOptions.Empty() {
				http.Error(w, "launcher does not support advanced session options", http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(request.Message) == "" {
				http.Error(w, "message is required", http.StatusBadRequest)
				return
			}
			alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
			if alias != "" && (strings.ContainsAny(alias, " \t\r\n/#") || len(alias) > 80) {
				http.Error(w, "name must be 1 to 80 characters without spaces, /, or #", http.StatusBadRequest)
				return
			}
			cwd, cwdErr := dashboardStartCwd(request.Cwd)
			if cwdErr != nil {
				http.Error(w, cwdErr.Error(), http.StatusBadRequest)
				return
			}
			adapter := d.surfaceForKind(surface.SurfaceKind(request.Surface))
			if adapter == nil {
				http.Error(w, "surface is not configured", http.StatusConflict)
				return
			}
			if err := d.Registry.EnsureAliasAvailable(alias); err != nil {
				var taken registry.AliasTakenError
				if errors.As(err, &taken) {
					http.Error(w, err.Error(), http.StatusBadRequest)
				} else {
					http.Error(w, fmt.Sprintf("check conversation name: %s", err), http.StatusInternalServerError)
				}
				return
			}
			if d.transportResolver == nil {
				http.Error(w, "launcher transport is unavailable", http.StatusConflict)
				return
			}
			launcher, launchErr := d.transportResolver.Require(request.Launcher, surface.SurfaceKind(request.Surface))
			if launchErr != nil {
				http.Error(w, launchErr.Error(), http.StatusBadRequest)
				return
			}
			available, detail := launcher.Available(r.Context())
			if !available {
				http.Error(w, detail, http.StatusConflict)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
			defer cancel()
			launchResult, launchErr := launcher.Launch(ctx, surface.LaunchRequest{Agent: surface.SurfaceKind(request.Surface), Cwd: cwd, Message: request.Message, Model: request.Model, Name: request.Name})
			if launchErr != nil {
				var acceptedErr surface.LaunchAcceptedError
				if errors.As(launchErr, &acceptedErr) {
					writeDashboardJSON(w, http.StatusAccepted, map[string]any{"ok": true, "status": "submitted", "accepted": true, "retryable": false, "launcher": request.Launcher, "warning": launchErr.Error()})
					return
				}
				http.Error(w, launchErr.Error(), http.StatusBadGateway)
				return
			}
			session, location, launchErr := locateLaunchedSession(ctx, launcher, adapter, launchResult)
			if launchErr != nil {
				writeAcceptedLaunch(w, request.Launcher, launchResult.Location, fmt.Sprintf("launcher accepted the session, but discovery failed: %s; check the catalog before retrying", launchErr))
				return
			}
			if session == nil {
				if location == nil {
					writeAcceptedLaunch(w, request.Launcher, nil, "launcher accepted the session, but its location is still unresolved; check the catalog before retrying")
					return
				}
				pendingID, err := d.Registry.RecordPendingLaunch(request.Launcher, surface.SurfaceKind(request.Surface), cwd, request.Name, alias, *location)
				if err != nil {
					writeAcceptedLaunch(w, request.Launcher, location, fmt.Sprintf("launcher accepted the session, but pending correlation could not be persisted: %s; check the catalog before retrying", err))
					return
				}
				_ = pendingID
				writeDashboardJSON(w, http.StatusCreated, map[string]any{"ok": true, "launcher": request.Launcher, "location": location})
				return
			}
			if session.Surface == "" {
				session.Surface = surface.SurfaceKind(request.Surface)
				session.Cwd = cwd
				session.Name = request.Name
			}
			session.Runtime = &surface.Runtime{Launcher: request.Launcher, Location: location, Focusable: location != nil}
			if err := d.Registry.RegisterSession(*session); err != nil {
				writeAcceptedLaunch(w, request.Launcher, location, fmt.Sprintf("launcher accepted the session, but registration failed: %s; check the catalog before retrying", err))
				return
			}
			if alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@"); alias != "" {
				if err := d.Registry.SetAlias(alias, session.ID); err != nil {
					writeAcceptedLaunch(w, request.Launcher, location, fmt.Sprintf("launcher accepted the session, but its name could not be persisted: %s; check the catalog before retrying", err))
					return
				}
			}
			writeDashboardJSON(w, http.StatusCreated, map[string]any{"ok": true, "launcher": request.Launcher, "location": location, "sessionId": session.ID})
			return
		}
		adapter := d.surfaceForKind(surface.SurfaceKind(request.Surface))
		starter, ok := adapter.(surface.SessionStarter)
		if adapter == nil || !ok {
			http.Error(w, "this surface cannot start conversations", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(request.Message) == "" {
			http.Error(w, "message is required", http.StatusBadRequest)
			return
		}
		cwd, cwdErr := dashboardStartCwd(request.Cwd)
		if cwdErr != nil {
			http.Error(w, cwdErr.Error(), http.StatusBadRequest)
			return
		}
		alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
		if err := d.Registry.EnsureAliasAvailable(alias); err != nil {
			var taken registry.AliasTakenError
			if errors.As(err, &taken) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else {
				http.Error(w, fmt.Sprintf("check conversation name: %s", err), http.StatusInternalServerError)
			}
			return
		}
		approval := strings.TrimSpace(request.Approval)
		if approval != "" && approval != "untrusted" && approval != "on-request" && approval != "never" {
			http.Error(w, "approval policy is invalid", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
		defer cancel()
		owner := ""
		if request.Surface == string(surface.KindCodex) {
			owner = "desktop"
		}
		session, sent, startErr := starter.StartSession(ctx, surface.SessionStartOptions{Message: request.Message, Cwd: cwd, Model: strings.TrimSpace(request.Model), ApprovalPolicy: approval, TurnOptions: request.TurnOptions, Name: request.Name, Worktree: request.Worktree, Agent: request.Agent, PermissionMode: request.PermissionMode, Owner: owner})
		if session != nil {
			session.Runtime = &surface.Runtime{Launcher: defaultLauncherForSurface(surface.SurfaceKind(request.Surface)), Focusable: false}
			if registerErr := d.Registry.RegisterSession(*session); registerErr != nil {
				writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but local registration failed: %s; do not retry automatically", registerErr))
				return
			}
			if alias != "" {
				if aliasErr := d.Registry.SetAlias(alias, session.ID); aliasErr != nil {
					writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but its name could not be persisted: %s; do not retry automatically", aliasErr))
					return
				}
			}
		}
		if startErr != nil {
			sessionID := ""
			if session != nil {
				sessionID = session.ID
			}
			if session != nil {
				intent, intentErr := d.Registry.RecordSessionCreationIntent(sessionID, request.Message)
				if intentErr != nil {
					_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: sessionID, Message: request.Message, Error: fmt.Sprintf("%s; durable delivery intent failed: %s", startErr, intentErr)})
					writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but its delivery intent could not be persisted: %s; do not retry automatically", intentErr))
					return
				}
				if surface.IsDeliveryOutcomeUnknown(startErr) {
					_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "submitted", SessionID: sessionID, Message: request.Message, Error: startErr.Error()})
					writeSubmittedSession(w, session, intent.ID, submittedSessionDetail(session, alias))
					return
				}
				_, _ = d.Registry.FailDeliveryIntent(intent.ID, registry.DeliveryIntentFailed, startErr.Error())
				_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: sessionID, Message: request.Message, Error: startErr.Error()})
				writeInitialSessionFailure(w, session, intent.ID, startErr.Error())
				return
			}
			unknown := surface.IsDeliveryOutcomeUnknown(startErr)
			kind := "failed"
			if unknown {
				kind = "unknown"
			}
			_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: kind, SessionID: sessionID, Message: request.Message, Error: startErr.Error()})
			writeDashboardJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "status": "failed", "retryable": false, "error": func() string {
				if unknown {
					return fmt.Sprintf("initial turn outcome is ambiguous, but no session identity was returned; inspect the provider before any explicit retry: %s", startErr)
				}
				return startErr.Error()
			}()})
			return
		}
		result := ""
		if sent != nil {
			result = sent.UUID
		}
		if result != "" {
			if runtimeErr := d.Registry.MarkDeliveryStarted(session.ID, result, ""); runtimeErr != nil {
				_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, Message: request.Message, Result: result, Error: runtimeErr.Error()})
			}
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "sent", SessionID: session.ID, Message: request.Message, Result: result})
		writeDashboardJSON(w, http.StatusCreated, map[string]any{"ok": true, "session": session, "result": sent})
		return
	}
	if request.Action == "notion-create" {
		ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
		defer cancel()
		receipt, createErr := d.createNotionThread(ctx, request.Message, request.Alias, request.Model)
		if createErr != nil {
			http.Error(w, createErr.Error(), http.StatusBadGateway)
			return
		}
		writeDashboardJSON(w, http.StatusCreated, map[string]any{"ok": true, "result": receipt, "sessionId": receipt.SessionID})
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
		adapter := d.surfaceForKind(target.Surface)
		if adapter == nil {
			http.Error(w, "target surface is not configured", http.StatusConflict)
			return
		}
		if err := d.ensureDashboardWritable(r.Context(), adapter, target); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
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
		sent, queued, submitted, failed := 0, 0, 0, 0
		for _, member := range members {
			session, sessionErr := d.Registry.Session(member)
			if sessionErr != nil {
				failed++
				continue
			}
			adapter := d.surfaceForKind(session.Surface)
			if adapter == nil {
				failed++
				continue
			}
			if err := d.ensureDashboardWritable(operationCtx, adapter, session); err != nil {
				failed++
				continue
			}
			receipt, deliverErr := (delivery.Dispatcher{Registry: d.Registry}).Deliver(operationCtx, adapter, session, request.Message, "")
			if deliverErr != nil {
				failed++
				continue
			}
			switch receipt.Evidence {
			case surface.EvidenceQueued:
				queued++
			case surface.EvidenceSubmitted:
				submitted++
			default:
				sent++
			}
		}
		if failed > 0 {
			http.Error(w, fmt.Sprintf("channel delivery: %d sent, %d queued, %d submitted, %d failed", sent, queued, submitted, failed), http.StatusConflict)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": sent, "queued": queued, "submitted": submitted, "failed": failed})
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
			adapter := d.surfaceForKind(target.Surface)
			if adapter == nil {
				http.Error(w, "target surface is not configured", http.StatusConflict)
				return
			}
			if err := d.ensureDashboardWritable(r.Context(), adapter, target); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
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
		adapter := d.surfaceForKind(toSession.Surface)
		if adapter == nil {
			http.Error(w, "destination surface is not configured", http.StatusConflict)
			return
		}
		if err := d.ensureDashboardWritable(r.Context(), adapter, toSession); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		pattern := request.Pattern
		if pattern == "" {
			pattern = ".*"
		}
		if _, err := d.Registry.AddRouteWithOptions(fromID, toID, pattern, request.Once); err != nil {
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
	if request.Action == "codex-launch" {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			http.Error(w, fmt.Sprintf("find Agenthail executable: %s", executableErr), http.StatusInternalServerError)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		output, launchErr := exec.CommandContext(ctx, executable, "launch", "codex").CombinedOutput()
		if launchErr != nil {
			detail := strings.TrimSpace(string(output))
			if detail == "" {
				detail = launchErr.Error()
			} else {
				detail += ": " + launchErr.Error()
			}
			http.Error(w, detail, http.StatusBadGateway)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": strings.TrimSpace(string(output))})
		return
	}
	if request.Action == "runtime-ensure" {
		adapter := d.surfaceForKind(surface.KindCodex)
		ensurer, ok := adapter.(surface.RuntimeEnsurer)
		if adapter == nil || !ok {
			http.Error(w, "Codex managed runtime cannot be repaired here", http.StatusConflict)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		if err := ensurer.EnsureRuntime(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
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
	if request.Action == "session-focus" {
		if session.Runtime == nil || !session.Runtime.Focusable || session.Runtime.Location == nil {
			http.Error(w, "session is not focusable", http.StatusConflict)
			return
		}
		if d.transportResolver == nil {
			http.Error(w, "launcher transport is unavailable", http.StatusConflict)
			return
		}
		launcher, ok := d.transportResolver.Launcher(session.Runtime.Launcher)
		focuser, focusable := launcher.(surface.Focuser)
		if !ok || !focusable {
			http.Error(w, "launcher cannot focus sessions", http.StatusConflict)
			return
		}
		adapter := d.surfaceForKind(session.Surface)
		if adapter == nil {
			session.Runtime.Focusable = false
			_ = d.Registry.SaveSessionRuntime(session.ID, session.Runtime)
			http.Error(w, "session surface is unavailable", http.StatusConflict)
			return
		}
		located, err := adapter.List(r.Context())
		if err != nil {
			session.Runtime.Focusable = false
			_ = d.Registry.SaveSessionRuntime(session.ID, session.Runtime)
			http.Error(w, "session location is unavailable", http.StatusConflict)
			return
		}
		locations := launcher.Locate(r.Context(), located)
		current, found := locations[session.ID]
		if !found || current != *session.Runtime.Location {
			session.Runtime.Focusable = false
			_ = d.Registry.SaveSessionRuntime(session.ID, session.Runtime)
			http.Error(w, "session location is stale", http.StatusConflict)
			return
		}
		if err := focuser.Focus(r.Context(), *session.Runtime.Location); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "launcher": session.Runtime.Launcher, "location": session.Runtime.Location, "sessionId": session.ID})
		return
	}
	if request.Action == "alias" {
		alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
		if alias == "" || strings.ContainsAny(alias, " \t\r\n/#") || len(alias) > 80 {
			http.Error(w, "name must be 1 to 80 characters without spaces, /, or #", http.StatusBadRequest)
			return
		}
		if err := d.Registry.ReplaceAlias(alias, session.ID); err != nil {
			http.Error(w, fmt.Sprintf("name conversation: %s", err), http.StatusBadRequest)
			return
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "identified", SessionID: session.ID, Result: "@" + alias})
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "alias": alias})
		return
	}
	adapter, transportErr := d.surfaceForSession(session)
	if transportErr != nil {
		http.Error(w, transportErr.Error(), http.StatusConflict)
		return
	}
	if request.Action == "session-fork" || request.Action == "native-queue" || strings.HasPrefix(request.Action, "session-lifecycle-") {
		d.dashboardSessionOperation(w, r, adapter, session, request.Action, request.Fork, request.NativeQueue)
		return
	}
	if err := d.ensureDashboardWritable(r.Context(), adapter, session); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	effective := surface.EffectiveCapabilities(session, adapter.Capabilities())
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	var result any
	switch request.Action {
	case "send":
		if strings.TrimSpace(request.Message) == "" {
			http.Error(w, "message is required", http.StatusBadRequest)
			return
		}
		if request.SourceSessionID != "" {
			if _, err := d.Registry.Session(request.SourceSessionID); err != nil {
				http.Error(w, "source session not found", http.StatusBadRequest)
				return
			}
		}
		receipt, actionErr := (delivery.Dispatcher{Registry: d.Registry}).DeliverWithOptions(ctx, adapter, session, request.Message, "", surface.SendOptions{Model: request.Model, SourceSessionID: request.SourceSessionID, BusyDelivery: request.BusyDelivery, TurnOptions: request.TurnOptions})
		if actionErr != nil {
			http.Error(w, actionErr.Error(), http.StatusBadGateway)
			return
		}
		result = receipt
	case "steer":
		if !effective.Steer || strings.TrimSpace(request.Message) == "" {
			http.Error(w, "this session cannot be steered", http.StatusBadRequest)
			return
		}
		result, err = (delivery.Dispatcher{Registry: d.Registry}).Steer(ctx, adapter, session, request.Message)
	case "interrupt":
		if !effective.Interrupt {
			http.Error(w, "this session cannot be interrupted", http.StatusBadRequest)
			return
		}
		err = adapter.Interrupt(ctx, session)
	case "compact":
		if !effective.Compact {
			http.Error(w, "this session cannot be compacted", http.StatusBadRequest)
			return
		}
		result, err = (delivery.Dispatcher{Registry: d.Registry}).Compact(ctx, adapter, session)
	case "goal-set", "goal-edit", "goal-pause", "goal-resume", "goal-budget":
		controller, ok := adapter.(surface.GoalController)
		if !effective.Goal || !ok {
			http.Error(w, "this session does not support typed goal controls", http.StatusBadRequest)
			return
		}
		var update surface.GoalUpdate
		switch request.Action {
		case "goal-set":
			objective := strings.TrimSpace(request.Message)
			if objective == "" {
				http.Error(w, "goal objective is required", http.StatusBadRequest)
				return
			}
			status := surface.GoalStatusActive
			update = surface.GoalUpdate{Objective: &objective, Status: &status}
		case "goal-edit":
			objective := strings.TrimSpace(request.Message)
			if objective == "" {
				http.Error(w, "goal objective is required", http.StatusBadRequest)
				return
			}
			update = surface.GoalUpdate{Objective: &objective}
		case "goal-pause", "goal-resume":
			status := surface.GoalStatusPaused
			if request.Action == "goal-resume" {
				status = surface.GoalStatusActive
			}
			update = surface.GoalUpdate{Status: &status}
		case "goal-budget":
			budget := strings.TrimSpace(request.Message)
			if budget == "" {
				update = surface.GoalUpdate{ClearTokenBudget: true}
				break
			}
			value, parseErr := strconv.ParseInt(budget, 10, 64)
			if parseErr != nil || value < 0 {
				http.Error(w, "token budget must be a non-negative integer", http.StatusBadRequest)
				return
			}
			update = surface.GoalUpdate{TokenBudget: &value}
		}
		err = controller.UpdateGoal(ctx, session, update)
	case "goal-clear":
		if !effective.Goal {
			http.Error(w, "this session does not support goals", http.StatusBadRequest)
			return
		}
		err = adapter.GoalClear(ctx, session)
	case "model":
		if !effective.Model {
			http.Error(w, "this session does not support model switching", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(request.Model) == "" {
			http.Error(w, "model is required", http.StatusBadRequest)
			return
		}
		result, err = adapter.Model(ctx, session, request.Model)
		if err == nil && session.Surface == surface.KindClaude {
			if persistErr := d.Registry.RegisterSession(*session); persistErr != nil {
				err = fmt.Errorf("persist Claude model state: %w", persistErr)
			}
		}
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

func dashboardStartCwd(value string) (string, error) {
	value = strings.TrimSpace(value)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	if value == "" || value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~/") {
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	value, err = filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	info, err := os.Stat(value)
	if err != nil {
		return "", fmt.Errorf("working directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working directory is not a directory")
	}
	return filepath.Clean(value), nil
}

func (d *Daemon) dashboardSearchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Get("surface") != "codex" {
		http.Error(w, "only Codex history search is available", http.StatusBadRequest)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) < 3 {
		http.Error(w, "search requires at least 3 characters", http.StatusBadRequest)
		return
	}
	adapter := d.surfaceForKind(surface.KindCodex)
	searcher, ok := adapter.(surface.SessionSearcher)
	if !ok {
		http.Error(w, "Codex history search is unavailable", http.StatusNotImplemented)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	stored, err := d.Registry.SearchSessions(surface.KindCodex, query, 20)
	if err != nil {
		http.Error(w, fmt.Sprintf("search saved Codex conversations: %s", err), http.StatusInternalServerError)
		return
	}
	results := make([]surface.SessionSearchResult, 0, len(stored)+20)
	for _, session := range stored {
		results = append(results, surface.SessionSearchResult{Session: session, Snippet: "Saved by Agenthail"})
	}
	remote, remoteErr := searcher.SearchSessions(ctx, query, 20)
	remoteError := ""
	if remoteErr != nil {
		if errors.Is(remoteErr, surface.ErrUnsupported) {
			remoteError = "Codex history search is unavailable in this Codex version"
		} else {
			remoteError = remoteErr.Error()
		}
	} else {
		results = mergeDashboardSearchResults(results, remote)
	}
	payload := make([]map[string]any, 0, len(results))
	for _, result := range results {
		if err := d.Registry.RegisterSession(result.Session); err != nil {
			http.Error(w, fmt.Sprintf("store search result: %s", err), http.StatusInternalServerError)
			return
		}
		alias, _ := d.Registry.ReverseAlias(result.Session.ID)
		effective := surface.EffectiveCapabilities(&result.Session, adapter.Capabilities())
		payload = append(payload, map[string]any{
			"session": dashboardSession{ID: result.Session.ID, Surface: result.Session.Surface, Name: result.Session.Name, Cwd: result.Session.Cwd, Alias: alias, Status: result.Session.Status, LastActive: result.Session.LastActive, Capabilities: effective.Capabilities, ReadOnly: effective.ReadOnly, ReadOnlyReason: effective.ReadOnlyReason, Source: result.Session.Source, Transport: result.Session.Transport},
			"snippet": result.Snippet,
		})
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"results": payload, "remoteError": remoteError})
}

func mergeDashboardSearchResults(left, right []surface.SessionSearchResult) []surface.SessionSearchResult {
	byID := make(map[string]surface.SessionSearchResult, len(left)+len(right))
	for _, result := range left {
		byID[result.Session.ID] = result
	}
	for _, result := range right {
		byID[result.Session.ID] = result
	}
	output := make([]surface.SessionSearchResult, 0, len(byID))
	for _, result := range byID {
		output = append(output, result)
	}
	sort.Slice(output, func(i, j int) bool { return output[i].Session.LastActive.After(output[j].Session.LastActive) })
	return output
}

func (d *Daemon) dashboardSessionHandler(w http.ResponseWriter, r *http.Request) {
	d.dashboardSessionHandlerWithTimeout(w, r, surfaceOperationTimeout)
}

func (d *Daemon) dashboardSessionHandlerWithTimeout(w http.ResponseWriter, r *http.Request, operationTimeout time.Duration) {
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
	limit := 20
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if parsed, parseErr := strconv.Atoi(rawLimit); parseErr == nil && parsed >= 4 && parsed <= 40 {
			limit = parsed
		}
	}
	alias, _ := d.Registry.ReverseAlias(session.ID)
	effective := surface.EffectiveCapabilities(session, adapter.Capabilities())
	var timelineBefore int64
	if raw := r.URL.Query().Get("timelineBefore"); raw != "" {
		var parseErr error
		timelineBefore, parseErr = strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || timelineBefore < 0 {
			http.Error(w, "invalid activity cursor", http.StatusBadRequest)
			return
		}
	}
	sessionRead, sessionReadErr := d.readJournalPage(session.ID, uint64(timelineBefore), limit)
	if sessionReadErr == nil && sessionRead.JournalSeq == 0 && timelineBefore == 0 {
		seedCtx, cancel := context.WithTimeout(r.Context(), min(operationTimeout, 2*time.Second))
		seedErr := d.sources.seed(seedCtx, session, adapter)
		cancel()
		sessionRead, sessionReadErr = d.readJournalPage(session.ID, 0, limit)
		if sessionReadErr == nil && seedErr != nil {
			sessionReadErr = seedErr
		}
	}
	var historyGap *registry.SessionJournalHistoryGapError
	if errors.As(sessionReadErr, &historyGap) {
		writeDashboardJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code":    "history_gap",
				"message": "The requested older session history is no longer retained.",
			},
			"earliestSeq": historyGap.EarliestSeq,
			"latestSeq":   historyGap.LatestSeq,
		})
		return
	}
	exchanges, transcript := truncateSessionExchanges(sessionRead.Exchanges)
	response := map[string]any{"session": session, "alias": alias, "exchanges": exchanges, "capabilities": effective.Capabilities, "readOnly": effective.ReadOnly, "readOnlyReason": effective.ReadOnlyReason, "readSource": sessionRead.Source, "transcriptTruncated": transcript.Truncated || sessionRead.Truncated, "transcriptOriginalBytes": transcript.OriginalBytes, "transcriptReturnedBytes": transcript.ReturnedBytes, "transcriptOriginalExchanges": transcript.OriginalExchanges, "transcriptReturnedExchanges": len(exchanges)}
	response["journalSeq"] = sessionRead.JournalSeq
	if sessionReadErr != nil {
		response["readError"] = "Session journal could not be read."
	} else if sessionRead.UnavailableReason != "" {
		response["readError"] = sessionRead.UnavailableReason
	}
	if r.URL.Query().Get("timeline") == "1" {
		response["timeline"] = &surface.SessionTimeline{Items: sessionRead.Items, NextBefore: sessionRead.NextBefore, Source: sessionRead.Source, Truncated: sessionRead.Truncated, UnavailableReason: sessionRead.UnavailableReason}
	}
	writeDashboardJSON(w, http.StatusOK, response)
}

func (d *Daemon) dashboardSessionMetadataHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, err := d.Registry.Session(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	adapter := d.surfaceForKind(session.Surface)
	if adapter == nil {
		http.Error(w, "surface is not configured", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	effective := surface.EffectiveCapabilities(session, adapter.Capabilities())
	requests := map[string]func() (any, error){}
	if provider, ok := adapter.(surface.ContextUsageProvider); ok {
		requests["context"] = func() (any, error) { return provider.ContextUsage(ctx, session) }
	}
	if effective.Goal {
		requests["goal"] = func() (any, error) { return adapter.GoalGet(ctx, session) }
	}
	if effective.Model {
		requests["model"] = func() (any, error) { return adapter.Model(ctx, session, "") }
		if lister, ok := adapter.(surface.ModelLister); ok {
			requests["models"] = func() (any, error) { return lister.Models(ctx) }
		}
	}
	if observer, ok := adapter.(surface.ClaudeRunObserver); ok {
		requests["claudeRuns"] = func() (any, error) {
			runs, err := observer.ObserveClaudeRuns(ctx)
			filtered := []surface.ClaudeRunObservation{}
			for _, run := range runs {
				if run.SessionID == session.ID || run.ResumeSessionID == session.ID {
					filtered = append(filtered, run)
				}
			}
			return filtered, err
		}
		requests["claudeSubagents"] = func() (any, error) {
			links, err := observer.ObserveClaudeSubagentLinks(ctx)
			filtered := []surface.ClaudeSubagentLink{}
			for _, link := range links {
				if link.ParentSessionID == session.ID {
					filtered = append(filtered, link)
				}
			}
			return filtered, err
		}
	}
	response := map[string]any{"sessionId": session.ID}
	type metadataResult struct {
		field string
		value any
		err   error
	}
	results := make(chan metadataResult, len(requests))
	for field, read := range requests {
		go func() { value, err := read(); results <- metadataResult{field: field, value: value, err: err} }()
	}
	errorsByField := map[string]string{}
	for len(requests) > 0 {
		select {
		case result := <-results:
			delete(requests, result.field)
			if result.err != nil {
				errorsByField[result.field] = "Metadata unavailable."
			} else {
				response[result.field] = result.value
			}
		case <-ctx.Done():
			for field := range requests {
				errorsByField[field] = "Metadata unavailable."
			}
			requests = nil
		}
	}
	if len(errorsByField) > 0 {
		response["errors"] = errorsByField
	}
	writeDashboardJSON(w, http.StatusOK, response)
}

type sessionTranscriptMetadata struct {
	Truncated         bool
	OriginalBytes     int
	ReturnedBytes     int
	OriginalExchanges int
}

func truncateSessionExchanges(exchanges []surface.Exchange) ([]surface.Exchange, sessionTranscriptMetadata) {
	metadata := sessionTranscriptMetadata{OriginalExchanges: len(exchanges)}
	for _, exchange := range exchanges {
		metadata.OriginalBytes += len(exchange.User) + len(exchange.Assistant)
	}
	remaining := sessionTranscriptJSONBudget
	result := make([]surface.Exchange, 0, len(exchanges))
	for index := len(exchanges) - 1; index >= 0 && remaining > 0; index-- {
		exchange := exchanges[index]
		userBudget := min(sessionTranscriptFieldLimit, remaining)
		if exchange.User != "" && exchange.Assistant != "" {
			userBudget = min(userBudget, remaining/2)
		}
		user, userEncoded, userTruncated := truncateJSONText(exchange.User, userBudget)
		remaining -= userEncoded
		assistant, assistantEncoded, assistantTruncated := truncateJSONText(exchange.Assistant, min(sessionTranscriptFieldLimit, remaining))
		remaining -= assistantEncoded
		if user == "" && assistant == "" && (exchange.User != "" || exchange.Assistant != "") {
			metadata.Truncated = true
			break
		}
		exchange.User = user
		exchange.Assistant = assistant
		metadata.ReturnedBytes += len(user) + len(assistant)
		metadata.Truncated = metadata.Truncated || userTruncated || assistantTruncated
		result = append(result, exchange)
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	metadata.Truncated = metadata.Truncated || len(result) < len(exchanges)
	return result, metadata
}

func truncateJSONText(value string, budget int) (string, int, bool) {
	if value == "" || budget <= 0 {
		return "", 0, value != ""
	}
	candidate := value
	truncated := false
	if len(candidate) > budget {
		candidate = validUTF8Prefix(candidate, budget)
		truncated = true
	}
	encoded, _ := json.Marshal(candidate)
	encodedBytes := max(0, len(encoded)-2)
	if encodedBytes <= budget {
		return candidate, encodedBytes, truncated
	}
	candidate = validUTF8Prefix(candidate, budget/6)
	encoded, _ = json.Marshal(candidate)
	return candidate, max(0, len(encoded)-2), true
}

func validUTF8Prefix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	if limit <= 0 {
		return ""
	}
	for limit > 0 && (value[limit]&0xc0) == 0x80 {
		limit--
	}
	return value[:limit]
}

func writeDashboardJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeSubmittedSession(w http.ResponseWriter, session *surface.Session, deliveryID int64, detail string) {
	body := map[string]any{
		"ok":        true,
		"status":    "submitted",
		"accepted":  true,
		"retryable": false,
		"session":   session,
		"detail":    detail,
	}
	if deliveryID > 0 {
		body["deliveryId"] = deliveryID
	}
	writeDashboardJSON(w, http.StatusAccepted, body)
}

func submittedSessionDetail(session *surface.Session, alias string) string {
	target := alias
	if target != "" {
		target = "@" + target
	} else {
		target = fmt.Sprintf("%s:%s", session.Surface, session.ID)
	}
	return "Submitted to " + target + "."
}

func writeInitialSessionFailure(w http.ResponseWriter, session *surface.Session, deliveryID int64, message string) {
	body := map[string]any{"ok": false, "status": "failed", "retryable": false, "session": session, "error": message}
	if deliveryID > 0 {
		body["deliveryId"] = deliveryID
	}
	writeDashboardJSON(w, http.StatusBadGateway, body)
}

func writeCreateStorageFailure(w http.ResponseWriter, session *surface.Session, warning string) {
	writeDashboardJSON(w, http.StatusInternalServerError, map[string]any{
		"ok":        false,
		"status":    "storage_failed",
		"retryable": false,
		"session":   session,
		"error":     warning,
	})
}

func writeAcceptedLaunch(w http.ResponseWriter, launcher string, location *surface.Location, warning string) {
	body := map[string]any{
		"ok":        true,
		"status":    "submitted",
		"accepted":  true,
		"retryable": false,
		"launcher":  launcher,
		"warning":   warning,
	}
	if location != nil {
		body["location"] = location
	}
	writeDashboardJSON(w, http.StatusAccepted, body)
}
