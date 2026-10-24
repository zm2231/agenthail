package daemon

import (
	"context"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/uiconf"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

//go:embed dashboard/index.html
var dashboardHTML []byte

//go:embed dashboard/app.js
var dashboardJS []byte

//go:embed dashboard/tokens.css
var dashboardCSS []byte

//go:embed dashboard/js/*.js dashboard/js/*.webmanifest
var dashboardModules embed.FS

var dashboardModuleTypes = map[string]string{
	".js":          "application/javascript; charset=utf-8",
	".webmanifest": "application/manifest+json",
}

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
	devices       browserDevices
	stateMu       sync.Mutex
	stateAt       time.Time
	stateVersion  atomic.Uint64
	cachedVersion uint64
	state         dashboardState
	voice         *voice.Service
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
	RuntimeKind       surface.RuntimeKind        `json:"runtimeKind,omitempty"`
	ConfiguredModel   string                     `json:"configuredModel,omitempty"`
	PendingModel      string                     `json:"pendingModel,omitempty"`
	ClaudeConfigRoot  *surface.SessionConfigRoot `json:"claudeConfigRoot,omitempty"`
	Name              string                     `json:"name"`
	Cwd               string                     `json:"cwd,omitempty"`
	Alias             string                     `json:"alias,omitempty"`
	Status            surface.SessionStatus      `json:"status"`
	WaitingOn         *surface.Waiting           `json:"waitingOn,omitempty"`
	BackgroundTasks   *surface.BackgroundTasks   `json:"backgroundTasks,omitempty"`
	UsageLimit        *surface.UsageLimit        `json:"usageLimit,omitempty"`
	LimitResume       *dashboardLimitResume      `json:"limitResume,omitempty"`
	Activity          *surface.Activity          `json:"activity,omitempty"`
	Plan              *surface.Plan              `json:"plan,omitempty"`
	LastActive        time.Time                  `json:"lastActive,omitempty"`
	QueueCount        int                        `json:"queueCount"`
	Open              bool                       `json:"open"`
	Current           bool                       `json:"current"`
	Archived          bool                       `json:"archived,omitempty"`
	CurrentReason     string                     `json:"currentReason,omitempty"`
	Capabilities      surface.Capabilities       `json:"capabilities"`
	ReadOnly          bool                       `json:"readOnly,omitempty"`
	ReadOnlyReason    string                     `json:"readOnlyReason,omitempty"`
	Source            string                     `json:"source,omitempty"`
	Transport         string                     `json:"transport,omitempty"`
	ForkedFrom        string                     `json:"forkedFrom,omitempty"`
	Project           *CatalogProject            `json:"project,omitempty"`
	Checkout          *catalogCheckout           `json:"checkout,omitempty"`
	ObservedAt        time.Time                  `json:"observedAt,omitempty"`
	UnavailableReason string                     `json:"unavailableReason,omitempty"`
	Runtime           *surface.Runtime           `json:"runtime,omitempty"`
	Freshness         *registry.CatalogFreshness `json:"freshness,omitempty"`
	Subagent          *surface.Subagent          `json:"subagent,omitempty"`
	Delegated         bool                       `json:"delegated,omitempty"`
	Subagents         *surface.SubagentRollup    `json:"subagents,omitempty"`
	Holders           []surface.Holder           `json:"holders,omitempty"`
	ObservedSubagents *surface.SubagentRollup    `json:"observedSubagents,omitempty"`
	HiddenSubagents   *surface.SubagentRollup    `json:"hiddenSubagents,omitempty"`
	// LiveHiddenSubagents is the part of HiddenSubagents that Subagents counts.
	LiveHiddenSubagents *surface.SubagentRollup `json:"liveHiddenSubagents,omitempty"`
	Listed              bool                    `json:"listed"`
	Host                *dashboardHost          `json:"host,omitempty"`
}

// dashboardPerson is what this host's sessions call the person, and whether
// they set it or it is their account's name.
type dashboardPerson struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

type dashboardState struct {
	UpdatedAt          time.Time                   `json:"updatedAt"`
	EventCursor        uint64                      `json:"eventCursor"`
	HostEpoch          string                      `json:"hostEpoch"`
	CatalogSeq         uint64                      `json:"catalogSeq"`
	Daemon             map[string]any              `json:"daemon"`
	Surfaces           []dashboardSurface          `json:"surfaces"`
	Sessions           []dashboardSession          `json:"sessions"`
	TotalSessions      int                         `json:"totalSessions"`
	Counts             map[string]int              `json:"counts,omitempty"`
	Projects           []dashboardProject          `json:"projects,omitempty"`
	NextCursor         string                      `json:"nextCursor,omitempty"`
	Queue              []dashboardQueue            `json:"queue"`
	Channels           []dashboardChannel          `json:"channels"`
	Relays             []dashboardRelay            `json:"relays"`
	History            []dashboardHistory          `json:"history"`
	Attention          []dashboardAttention        `json:"attention"`
	DeliveryProblems   []registry.DeliveryProblem  `json:"deliveryProblems"`
	CodexRecentHours   int                         `json:"codexRecentHours"`
	BusyDelivery       string                      `json:"busyDelivery"`
	Signin             []signin.Status             `json:"signin"`
	Rekeyed            map[string]string           `json:"rekeyed,omitempty"`
	Coordinators       []registry.CoordinatorEntry `json:"coordinators"`
	Person             dashboardPerson             `json:"person"`
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
	HeldBy          registry.QueueHold       `json:"heldBy,omitempty"`
	Historical      bool                     `json:"historical"`
	Evidence        surface.DeliveryEvidence `json:"evidence"`
	Operation       registry.QueueOperation  `json:"operation"`
	BusyDelivery    string                   `json:"busyDelivery,omitempty"`
}

func (d *Daemon) dashboardQueueItem(item registry.QueueRow) dashboardQueue {
	return dashboardQueue{TurnOptions: item.TurnOptions, ID: item.ID, SessionID: d.publicSessionID(item.SessionID), SourceSessionID: d.publicSessionID(item.SourceSessionID), Target: d.resolveDisplay(item.SessionID), Message: item.Message, Model: item.Model, Status: item.Status, Attempts: item.Attempts, LastError: item.LastError, QueuedAt: item.QueuedAt, HeldBy: item.HeldBy, Historical: item.Historical, Evidence: item.Evidence, Operation: item.Operation, BusyDelivery: item.BusyDelivery}
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
	Handoff      string `json:"handoff"`
	EventCursor  int64  `json:"eventCursor"`
	PendingCount int64  `json:"pendingCount"`
	LastError    string `json:"lastError,omitempty"`
	ID           int64  `json:"id"`
	From         string `json:"from"`
	To           string `json:"to"`
	Pattern      string `json:"pattern"`
	Once         bool   `json:"once"`
	Active       bool   `json:"active"`
	FireCount    int64  `json:"fireCount"`
	LastFiredAt  string `json:"lastFiredAt,omitempty"`
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
	Operation       registry.QueueOperation  `json:"operation,omitempty"`
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

// dashboardShutdownTimeout bounds how long a stopping daemon waits for the
// requests in flight, which may still write to the registry, to finish.
const dashboardShutdownTimeout = 5 * time.Second

func (d *dashboardServer) shutdown() error {
	if d.cancel != nil {
		d.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), dashboardShutdownTimeout)
	defer cancel()
	err := d.server.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		err = d.server.Close()
	}
	if d.voice != nil {
		d.voice.Close()
	}
	return err
}

func (d *Daemon) dashboardHandler(dashboard *dashboardServer) http.Handler {
	d.dashboard = dashboard
	dashboard.devices = d.Registry
	mux := http.NewServeMux()
	mux.HandleFunc("/", dashboard.page)
	mux.HandleFunc("/app.js", dashboard.asset("application/javascript; charset=utf-8", dashboardJS))
	mux.Handle("/js/", dashboardModuleHandler())
	mux.Handle("/css/", dashboardStylesHandler())
	mux.HandleFunc("/tokens.css", dashboard.asset("text/css; charset=utf-8", dashboardCSS))
	mux.HandleFunc("/palette.css", dashboard.asset("text/css; charset=utf-8", []byte(uiconf.PaletteCSS())))
	mux.HandleFunc("/logo.png", dashboard.asset("image/png", dashboardLogo))
	mux.HandleFunc("/favicon.png", dashboard.asset("image/png", dashboardFavicon))
	mux.HandleFunc("/favicon.ico", dashboard.asset("image/png", dashboardFavicon))
	mux.HandleFunc("/api/state", dashboard.guard(func(w http.ResponseWriter, r *http.Request) { d.dashboardStateCached(dashboard, w, r) }))
	mux.HandleFunc("/api/session", dashboard.guard(d.hostRouted("id", d.dashboardSessionHandler)))
	mux.HandleFunc("/api/session-metadata", dashboard.guard(d.hostRouted("id", d.dashboardSessionMetadataHandler)))
	mux.HandleFunc("/api/session-attachment", dashboard.guard(d.hostRouted("sessionId", d.apiSessionAttachmentHandler)))
	mux.HandleFunc("/api/session-stream", dashboard.guard(d.hostRouted("id", d.apiSessionStreamHandler)))
	mux.HandleFunc("/api/models", dashboard.guard(d.hostRouted("id", d.dashboardModelsHandler)))
	mux.HandleFunc("/api/requests", dashboard.guard(d.hostRouted("sessionId", d.requestsHandler)))
	mux.HandleFunc("/api/search", dashboard.guard(d.dashboardSearchHandler))
	mux.HandleFunc("/api/history", dashboard.guard(d.hostRouted("sessionId", d.dashboardHistoryHandler)))
	mux.HandleFunc("/api/action", dashboard.guard(d.idempotentActionHandler(d.hostRoutedAction(d.dashboardActionHandler))))
	mux.HandleFunc("/api/settings", dashboard.guard(d.dashboardSettingsHandler))
	mux.HandleFunc("/api/claude/compact-settings", dashboard.guard(d.claudeCompactSettingsHandler))
	mux.HandleFunc("/api/settings/remote-qr", dashboard.guard(d.dashboardRemoteQRHandler))
	d.registerAPIV1(mux, dashboard)
	return d.dashboardHeaders(mux)
}

func (d *Daemon) dashboardModelsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	adapter := d.adapters().ForSurface(surface.SurfaceKind(r.URL.Query().Get("surface")))
	lister, ok := adapter.(surface.ModelLister)
	if adapter == nil || !ok {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "this surface does not list models")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	models, err := lister.Models(ctx)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "bad_gateway", fmt.Sprintf("list models: %s", err))
		return
	}
	if models == nil {
		models = []surface.ModelOption{}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (d *Daemon) person() (dashboardPerson, error) {
	setting, err := d.Registry.PersonNameSetting()
	if err != nil {
		return dashboardPerson{}, err
	}
	name, err := d.Registry.PersonName()
	return dashboardPerson{Name: name, Set: setting != ""}, err
}

func (d *Daemon) dashboardSettingsHandler(w http.ResponseWriter, r *http.Request) {
	config, err := LoadDashboardConfig()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if r.Method == http.MethodGet {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"dashboard": config, "remoteAccess": RemoteAccessStatusForConfig(config), "notifications": GetNotificationStatus(), "daemon": map[string]any{"pid": os.Getpid(), "running": true}})
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
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
		PersonName       string `json:"personName"`
		ResumeAfterLimit bool   `json:"resumeAfterLimit"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid settings request")
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
		if err = SaveDashboardConfig(config); err == nil {
			d.publishCatalogPresence()
		}
	case "resume-after-limit":
		config.ResumeAfterLimit = &request.ResumeAfterLimit
		err = SaveDashboardConfig(config)
	case "person-name":
		err = d.Registry.SetPersonName(request.PersonName)
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
			err = Notify("Agenthail", "Notifications are working", "", "")
		}
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "unsupported settings action")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d *Daemon) dashboardRemoteQRHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	link, err := d.createLoginLink(true)
	if err == errRemoteAccessOff {
		writeAPIError(w, http.StatusConflict, "conflict", "remote access is not enabled")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	image, err := RemoteAccessQR(link.URL)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(image)
}

func (d *Daemon) dashboardHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	limit := 25
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	maxLimit := 100
	if sessionID != "" {
		maxLimit = 200
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxLimit {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("limit must be between 1 and %d", maxLimit))
			return
		}
		limit = parsed
	}
	var beforeID int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "before must be a positive history id")
			return
		}
		beforeID = parsed
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	queryText := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(kind) > 100 || len(queryText) > 200 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "history filters are too long")
		return
	}
	var entries []registry.HistoryEntry
	hasMore := false
	var err error
	if sessionID != "" {
		if beforeID != 0 || kind != "" || queryText != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "a session's history takes only a limit")
			return
		}
		entries, err = d.Registry.ListHistory(limit, sessionID)
	} else {
		entries, hasMore, err = d.Registry.ListHistoryPage(limit, beforeID, kind, queryText)
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("read delivery history: %s", err))
		return
	}
	kinds, err := d.Registry.ListHistoryKinds()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("read delivery history kinds: %s", err))
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
	return dashboardHistory{ID: entry.ID, CreatedAt: entry.CreatedAt, Kind: entry.Kind, SessionID: d.publicSessionID(entry.SessionID), SourceSessionID: d.publicSessionID(entry.SourceSessionID), Target: d.resolveDisplay(entry.SessionID), Source: d.resolveDisplay(entry.SourceSessionID), QueueID: entry.QueueID, Message: entry.Message, Result: entry.Result, Error: entry.Error, Evidence: entry.Evidence, Operation: entry.Operation}
}

func (d *Daemon) dashboardStateCached(dashboard *dashboardServer, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
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
	cacheable := page == nil && !catalogMirror(r.Context())
	hostEpoch, catalogSeq, catalogErr := d.Registry.CatalogState()
	dashboard.stateMu.Lock()
	version := dashboard.stateVersion.Load()
	if cacheable && catalogErr == nil && r.URL.Query().Get("fresh") != "1" && dashboard.cachedVersion == version && dashboard.state.HostEpoch == hostEpoch && dashboard.state.CatalogSeq == catalogSeq && !dashboard.stateAt.IsZero() && time.Since(dashboard.stateAt) < dashboardStateCacheTTL {
		state, tag := dashboard.state, dashboardSnapshotTag(dashboard.state, dashboard.cachedVersion, dashboard.stateAt)
		dashboard.stateMu.Unlock()
		if r.URL.RawQuery == "" {
			w.Header().Set("ETag", tag)
			if r.Header.Get("If-None-Match") == tag {
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
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
		if cacheable && hadPrevious {
			stale := previous
			stale.Daemon = cloneDashboardDaemon(stale.Daemon)
			stale.Daemon["stale"] = true
			stale.Daemon["refreshError"] = err.Error()
			d.writeDashboardSnapshot(w, r, stale)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if cacheable {
		dashboard.stateMu.Lock()
		dashboard.state = state
		dashboard.stateAt = time.Now()
		dashboard.cachedVersion = version
		tag := dashboardSnapshotTag(state, version, dashboard.stateAt)
		dashboard.stateMu.Unlock()
		if r.URL.RawQuery == "" {
			w.Header().Set("ETag", tag)
		}
	}
	d.writeDashboardSnapshot(w, r, state)
}

// dashboardSnapshotTag names one cached snapshot: the cached state is never
// changed once stored, so its catalog position, state version and build time
// identify the body a plain snapshot request receives.
func dashboardSnapshotTag(state dashboardState, version uint64, builtAt time.Time) string {
	return fmt.Sprintf(`"%s-%d-%d-%d"`, state.HostEpoch, state.CatalogSeq, version, builtAt.UnixNano())
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
	if !validCatalogScope(scope) {
		writeAPIError(w, http.StatusBadRequest, "invalid_scope", "Use running, current, recent, all, or archived.")
		return
	}
	projectID, query := r.URL.Query().Get("projectId"), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	rows := make([]dashboardSession, 0, len(state.Sessions))
	for _, row := range state.Sessions {
		if scope != "" && !catalogScopeMatches(scope, row) {
			continue
		}
		if projectID != "" && (row.Project == nil || row.Project.ID != projectID) {
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
	if !validCatalogScope(scope) {
		return nil, fmt.Errorf("use running, current, recent, all, or archived")
	}
	projectID, query := r.URL.Query().Get("projectId"), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	parentID := r.URL.Query().Get("parentId")
	filter := scope + "\x00" + projectID + "\x00" + query + "\x00" + parentID
	request := &registry.CatalogPageRequest{Scope: scope, ProjectID: projectID, ParentID: parentID, Query: query, Limit: limit}
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
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

func dashboardModuleHandler() http.Handler {
	modules, err := fs.Sub(dashboardModules, "dashboard/js")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/js/", http.FileServerFS(modules))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		contentType, ok := dashboardModuleTypes[path.Ext(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}

func (dashboard *dashboardServer) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeAPIError(w, http.StatusNotFound, "unknown_route", "This Agenthail has no "+r.URL.Path+"; it runs an older version.")
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if secret := r.URL.Query().Get("login"); secret != "" {
		dashboard.login(w, r, secret)
		return
	}
	if !dashboard.authorized(r) {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "sign in with a link from 'agenthail dashboard'")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(dashboardHTML)
}

func (dashboard *dashboardServer) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !dashboard.authorized(r) {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "dashboard access token required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeAPIError(w, http.StatusForbidden, "forbidden", "cross-origin dashboard request rejected")
			return
		}
		next(w, r)
	}
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host
}

func (d *Daemon) dashboardHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hosts.ResponseHeader, d.hostIdentity().ID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; connect-src 'self' https: wss:; media-src blob:; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
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
	limits, err := d.limitResumeRows()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read usage-limit stops: %w", err)
	}
	aliases, err := d.Registry.ListAliases()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read aliases: %w", err)
	}
	aliasByID := make(map[string]string, len(aliases))
	for _, alias := range aliases {
		aliasByID[alias.SessionID] = alias.Name
	}
	queue, err := d.Registry.ListQueue(false, d.adapters())
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
	for index := range deliveryProblems {
		deliveryProblems[index].SessionID = d.publicSessionID(deliveryProblems[index].SessionID)
		deliveryProblems[index].SourceSessionID = d.publicSessionID(deliveryProblems[index].SourceSessionID)
		if deliveryProblems[index].AttentionSessionID != "" {
			deliveryProblems[index].AttentionSessionID = d.publicSessionID(deliveryProblems[index].AttentionSessionID)
		}
	}
	rekeyed, err := d.Registry.SessionRekeys()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read session rekeys: %w", err)
	}
	view, err := d.loadCatalogViewState()
	if err != nil {
		return dashboardState{}, err
	}
	projectRecords, err := d.Registry.ListProjects()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read projects: %w", err)
	}
	coordinators, err := d.Registry.LocalCoordinators()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read coordinators: %w", err)
	}
	person, err := d.person()
	if err != nil {
		return dashboardState{}, fmt.Errorf("read the person's name: %w", err)
	}
	state := dashboardState{UpdatedAt: now.UTC(), EventCursor: eventCursor, HostEpoch: catalogSnapshot.HostEpoch, CatalogSeq: catalogSnapshot.CatalogSeq, Daemon: map[string]any{"running": true, "pid": os.Getpid(), "host": d.localHost(), "hosts": d.hostSummaries()}, Surfaces: make([]dashboardSurface, 0, len(d.Surfaces)), Sessions: []dashboardSession{}, Queue: make([]dashboardQueue, 0, len(queue)), Channels: make([]dashboardChannel, 0, len(channels)), Relays: make([]dashboardRelay, 0, len(routes)), History: make([]dashboardHistory, 0, len(history)), Attention: make([]dashboardAttention, 0, len(attention)), DeliveryProblems: deliveryProblems, CodexRecentHours: config.CodexRecentHours, BusyDelivery: string(config.BusyDelivery), Signin: make([]signin.Status, 0, len(signin.Consumers)), Rekeyed: rekeyed, Person: person}
	for _, consumer := range signin.Consumers {
		state.Signin = append(state.Signin, d.signinConsumer(consumer))
	}
	state.Coordinators = make([]registry.CoordinatorEntry, 0, len(coordinators))
	for _, spec := range coordinators {
		state.Coordinators = append(state.Coordinators, spec.Entry())
	}
	if page != nil {
		state.catalogPageApplied = true
		state.catalogPageFilter = pageRequest[0].Scope + "\x00" + pageRequest[0].ProjectID + "\x00" + pageRequest[0].Query + "\x00" + pageRequest[0].ParentID
		state.catalogPageOffset = page.Offset
		state.catalogPageLimit = page.Limit
		state.catalogPageHasMore = page.HasMore
		state.UpdatedAt = pageRequest[0].Now.UTC()
	}
	for _, item := range queue {
		state.Queue = append(state.Queue, d.dashboardQueueItem(item))
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
		state.Relays = append(state.Relays, dashboardRelay{ID: route.ID, From: d.resolveDisplay(route.FromSession), To: d.resolveDisplay(route.ToSession), Pattern: route.Pattern, Handoff: route.Handoff, EventCursor: route.EventCursor, PendingCount: route.PendingCount, LastError: route.LastError, Once: route.Once, Active: route.Active, FireCount: route.FireCount, LastFiredAt: route.LastFiredAt})
	}
	for _, entry := range history {
		state.History = append(state.History, d.dashboardHistoryEntry(entry))
	}
	for _, item := range attention {
		state.Attention = append(state.Attention, dashboardAttention{ID: item.ID, SessionID: d.publicSessionID(item.SessionID), Target: d.resolveDisplay(item.SessionID), QueueID: item.QueueID, Reason: item.Reason, RequestedAction: item.RequestedAction, CreatedAt: item.CreatedAt})
	}
	catalogSurface := make(map[surface.SurfaceKind]registry.CatalogSurfaceState, len(catalogSnapshot.Surfaces))
	for _, record := range catalogSnapshot.Surfaces {
		catalogSurface[record.Surface] = record
	}
	for _, adapter := range d.Surfaces {
		entry := dashboardSurface{Name: string(adapter.Name()), Connected: false, Health: "unknown", Capabilities: adapter.Capabilities()}
		if record, found := catalogSurface[adapter.Name()]; found {
			entry.Health = record.Health
			entry.HealthDetail = record.Detail
			entry.Connected = record.Health == "healthy"
		}
		if observed, found := d.observedSurfaceHealth(adapter.Name()); found {
			entry.Health = observed.Health
			entry.HealthDetail = observed.HealthDetail
			entry.Runtime = observed.Runtime
			entry.RepairAction = observed.RepairAction
			entry.RepairLabel = observed.RepairLabel
		}
		state.Surfaces = append(state.Surfaces, entry)
	}
	catalogSessions := map[string]registry.CatalogSessionState{}
	sessions := make([]surface.Session, 0, len(catalogSnapshot.Sessions))
	mirror := catalogMirror(ctx)
	for _, record := range catalogSnapshot.Sessions {
		if !mirror && (catalogSnapshot.Listing.Hidden[record.Session.ID] || page == nil && catalogSnapshot.Listing.Retired[record.Session.ID]) {
			continue
		}
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
	local := make([]int, 0, len(sessions))
	for _, session := range sessions {
		if record, found := catalogSessions[session.ID]; found && record.HostID != "" {
			var mirrored dashboardSession
			if json.Unmarshal([]byte(record.ProjectionFingerprint), &mirrored) != nil {
				continue
			}
			mirrored.ObservedAt = record.ObservedAt
			mirrored.Freshness = &record.Freshness
			mirrored.HiddenSubagents, mirrored.LiveHiddenSubagents = catalogSnapshot.Listing.HiddenSubagents(session.ID), catalogSnapshot.Listing.LiveHiddenSubagents(session.ID)
			mirrored.Subagents = addSubagentRollups(mirrored.ObservedSubagents, mirrored.LiveHiddenSubagents)
			state.Sessions = append(state.Sessions, mirrored)
			continue
		}
		adapter, adapterErr := d.adapters().For(&session)
		if adapterErr != nil {
			continue
		}
		identity := catalogIdentity{}
		observedAt := time.Time{}
		if record, found := catalogSessions[session.ID]; found {
			_ = json.Unmarshal(record.Project, &identity.Project)
			_ = json.Unmarshal(record.Checkout, &identity.Checkout)
			identity.UnavailableReason = record.UnavailableReason
			observedAt = record.ObservedAt
		}
		if observedAt.IsZero() {
			observedAt = now.UTC()
		}
		entry := d.catalogSessionProjection(adapter, session, identity, observedAt, aliasByID[session.ID], counts[session.ID], limits[session.ID], false, config)
		if record, found := catalogSessions[session.ID]; found {
			entry.Freshness = &record.Freshness
		}
		entry.QueueCount = counts[session.ID]
		if record, found := catalogSessions[session.ID]; found && record.ProjectionFingerprint != "" {
			var saved dashboardSession
			if json.Unmarshal([]byte(record.ProjectionFingerprint), &saved) == nil {
				entry.Open, entry.Listed = saved.Open, saved.Listed
				entry.ObservedSubagents = saved.ObservedSubagents
				entry.Current, entry.CurrentReason = saved.Current, saved.CurrentReason
				entry.Holders = saved.Holders
				entry.Activity = saved.Activity
				entry.Plan = saved.Plan
			}
		}
		entry.HiddenSubagents, entry.LiveHiddenSubagents = catalogSnapshot.Listing.HiddenSubagents(session.ID), catalogSnapshot.Listing.LiveHiddenSubagents(session.ID)
		entry.Subagents = addSubagentRollups(entry.ObservedSubagents, entry.LiveHiddenSubagents)
		local = append(local, len(state.Sessions))
		state.Sessions = append(state.Sessions, entry)
	}
	localRows := make([]dashboardSession, 0, len(local))
	for _, index := range local {
		localRows = append(localRows, state.Sessions[index])
	}
	d.settleArchiveActivity(localRows, view)
	for _, index := range local {
		applyCatalogView(&state.Sessions[index], view)
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
		state.Counts = catalogScopeCounts(state.Sessions)
		coordinators, err := d.Registry.Coordinators()
		if err != nil {
			return dashboardState{}, fmt.Errorf("read coordinators: %w", err)
		}
		state.Projects = catalogProjects(state.Sessions, projectRecords, coordinators, view.archive.Projects, d.localHost(), d.coordinatorHostRef)
	}
	return state, nil
}

func (d *Daemon) recordSurfaceHealth(ctx context.Context, adapter surface.Surface, listErr error) dashboardSurface {
	entry := d.dashboardSurfaceHealth(ctx, adapter, listErr)
	d.surfaceHealthMu.Lock()
	defer d.surfaceHealthMu.Unlock()
	if d.surfaceHealth == nil {
		d.surfaceHealth = map[surface.SurfaceKind]dashboardSurface{}
	}
	d.surfaceHealth[adapter.Name()] = entry
	return entry
}

func (d *Daemon) observedSurfaceHealth(kind surface.SurfaceKind) (dashboardSurface, bool) {
	d.surfaceHealthMu.Lock()
	defer d.surfaceHealthMu.Unlock()
	entry, found := d.surfaceHealth[kind]
	return entry, found
}

func (d *Daemon) dashboardSurfaceHealth(ctx context.Context, adapter surface.Surface, listErr error) dashboardSurface {
	entry := dashboardSurface{Name: string(adapter.Name()), Connected: listErr == nil, Health: "healthy", Capabilities: adapter.Capabilities()}
	if signin.IsOff(listErr) {
		entry.Health = "signin_off"
		entry.HealthDetail = listErr.Error()
		return entry
	}
	if signin.IsUnavailable(listErr) {
		entry.Health = "signin_unavailable"
		entry.HealthDetail = listErr.Error()
		return entry
	}
	if listErr != nil {
		entry.Error = listErr.Error()
		entry.Health = "unavailable"
		entry.HealthDetail = listErr.Error()
	}
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if checker, ok := adapter.(surface.HealthChecker); ok {
		if err := checker.Health(healthCtx); signin.IsUnavailable(err) {
			entry.Health = "signin_unavailable"
			entry.HealthDetail = err.Error()
		} else if err != nil {
			if entry.Connected {
				entry.Health = "degraded"
			}
			entry.HealthDetail = err.Error()
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
			}
			if entry.Health == "healthy" && entry.HealthDetail == "" {
				messages := make([]string, 0, len(runtimeStatus.Notes))
				for _, note := range runtimeStatus.Notes {
					messages = append(messages, note.Message)
				}
				entry.HealthDetail = strings.Join(messages, "; ")
			}
			switch runtimeStatus.Problem {
			case surface.RuntimeBridgeUnavailable:
				entry.RepairAction = "codex-launch"
				entry.RepairLabel = "Launch Codex through Agenthail"
			case surface.RuntimeStopped:
				entry.RepairAction = "runtime-ensure"
				entry.RepairLabel = "Start Codex app-server daemon"
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

func (d *Daemon) dashboardActionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
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
		ConfigRoot      string                     `json:"configRoot"`
		StopTerminal    bool                       `json:"stopTerminal"`
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
		NewChannel      string                     `json:"newChannel"`
		TargetID        string                     `json:"targetId"`
		FromID          string                     `json:"fromId"`
		ToID            string                     `json:"toId"`
		Pattern         string                     `json:"pattern"`
		Handoff         string                     `json:"handoff"`
		Once            bool                       `json:"once"`
		RelayID         int64                      `json:"relayId"`
		RelayHops       int                        `json:"relayHops"`
		DeliveryID      int64                      `json:"deliveryId"`
		Surface         string                     `json:"surface"`
		Cwd             string                     `json:"cwd"`
		Launcher        string                     `json:"launcher"`
		Approval        string                     `json:"approvalPolicy"`
		Author          surface.Author             `json:"author"`
		PersonName      string                     `json:"personName"`
		PID             int                        `json:"pid"`
		PIDStartedAt    int64                      `json:"pidStartedAt"`
		ProjectID       string                     `json:"projectId"`
		Path            string                     `json:"path"`
		Title           string                     `json:"title"`
		Attachments     []string                   `json:"attachments"`
		RequestID       string                     `json:"requestId"`
		Decision        surface.Decision           `json:"decision"`
		Instructions    *string                    `json:"instructions"`
		Replace         bool                       `json:"replace"`
		ChangedAt       time.Time                  `json:"changedAt"`
		ChangedBy       string                     `json:"changedBy"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 140<<10)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid dashboard request")
		return
	}
	if len(request.Attachments) > 0 && request.Action != "send" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "attachments are only accepted with send")
		return
	}
	if strings.HasPrefix(request.Action, "project-") {
		d.projectAction(r, w, projectRequest{Action: request.Action, ProjectID: request.ProjectID, Path: request.Path, Title: request.Title})
		return
	}
	if strings.HasPrefix(request.Action, "coordinator-") {
		d.coordinatorAction(r, w, coordinatorRequest{Action: request.Action, SessionID: request.SessionID, SourceSessionID: request.SourceSessionID, ProjectID: request.ProjectID, Name: request.Name, Surface: request.Surface, Cwd: request.Cwd, Path: request.Path, Model: request.Model, PermissionMode: request.PermissionMode, Instructions: request.Instructions, Replace: request.Replace, ChangedAt: request.ChangedAt, ChangedBy: request.ChangedBy, Message: request.Message, Author: request.Author, PersonName: request.PersonName})
		return
	}
	if request.Action == "delivery-dismiss" {
		if request.DeliveryID <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "deliveryId is required")
			return
		}
		dismissed, err := d.Registry.DismissDeliveryProblem(request.DeliveryID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeAPIError(w, http.StatusNotFound, "not_found", "delivery problem not found")
				return
			}
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if err := d.catalog.flushCommitted(); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("publish delivery dismissal: %s", err))
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "dismissed": dismissed})
		return
	}
	if request.Action == "session-create" {
		approval := strings.TrimSpace(request.Approval)
		if approval != "" && approval != "untrusted" && approval != "on-request" && approval != "never" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "approval policy is invalid")
			return
		}
		creator := strings.TrimSpace(request.SourceSessionID)
		if creator != "" {
			if _, err := d.Registry.Session(creator); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("sourceSessionId %s is not a session here", creator))
				return
			}
		}
		startOptions := func(cwd string) surface.SessionStartOptions {
			return surface.SessionStartOptions{Message: request.Message, Cwd: cwd, Model: strings.TrimSpace(request.Model), ApprovalPolicy: approval, TurnOptions: request.TurnOptions, Name: request.Name, Worktree: request.Worktree, Agent: request.Agent, PermissionMode: request.PermissionMode, ConfigRoot: strings.TrimSpace(request.ConfigRoot)}
		}
		if request.Launcher != "" {
			if strings.TrimSpace(request.Message) == "" {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "message is required")
				return
			}
			alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
			if alias != "" && (strings.ContainsAny(alias, " \t\r\n/#") || len(alias) > 80) {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "name must be 1 to 80 characters without spaces, /, or #")
				return
			}
			cwd, cwdErr := dashboardStartCwd(request.Cwd)
			if cwdErr != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", cwdErr.Error())
				return
			}
			adapter := d.adapters().ForSurface(surface.SurfaceKind(request.Surface))
			if adapter == nil {
				writeAPIError(w, http.StatusConflict, "conflict", "surface is not configured")
				return
			}
			reservation, reserved := d.reserveStartName(w, alias)
			if !reserved {
				return
			}
			keepReservation := false
			defer func() {
				if !keepReservation {
					_ = d.Registry.ReleaseAliasReservation(reservation)
				}
			}()
			if d.transportResolver == nil {
				writeAPIError(w, http.StatusConflict, "conflict", "launcher transport is unavailable")
				return
			}
			launcher, launchErr := d.transportResolver.Require(request.Launcher, surface.SurfaceKind(request.Surface))
			if launchErr != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", launchErr.Error())
				return
			}
			available, detail := launcher.Available(r.Context())
			if !available {
				writeAPIError(w, http.StatusConflict, "conflict", detail)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
			defer cancel()
			messageUUID := uuid.NewString()
			ctx = surface.WithMessageUUID(ctx, messageUUID)
			launchedAt := time.Now()
			// A launch that may be running but has no session yet stays pending,
			// holding its name, until discovery finds it at its location.
			pend := func(location *surface.Location, detail string) {
				if location == nil || *location == (surface.Location{}) {
					d.writeAcceptedLaunch(w, request.Launcher, location, detail, untracked(alias))
					return
				}
				if _, err := d.Registry.RecordPendingLaunch(request.Launcher, surface.SurfaceKind(request.Surface), cwd, request.Name, reservation, *location, launchedAt, launchedAt.Add(pendingLaunchLifetime)); err != nil {
					failure := fmt.Sprintf("persist pending correlation: %s", err)
					if detail != "" {
						failure = detail + "; " + failure
					}
					d.writeAcceptedLaunch(w, request.Launcher, location, failure, untracked(alias))
					return
				}
				keepReservation = true
				if detail != "" {
					d.writeAcceptedLaunch(w, request.Launcher, location, detail, "")
					return
				}
				writeDashboardJSON(w, http.StatusCreated, map[string]any{"ok": true, "launcher": request.Launcher, "location": location})
			}
			launchResult, launchErr := launcher.Launch(ctx, surface.LaunchRequest{Surface: surface.SurfaceKind(request.Surface), Options: startOptions(cwd)})
			startErr := launchErr
			if launchErr != nil && launchResult.Session == nil {
				var acceptedErr surface.LaunchAcceptedError
				switch {
				case errors.Is(launchErr, surface.ErrLaunchOptionUnsupported):
					writeAPIError(w, http.StatusBadRequest, "invalid_request", launchErr.Error())
					return
				case errors.As(launchErr, &acceptedErr):
					pend(launchResult.Location, launchErr.Error())
					return
				default:
					d.writeUnstartedSession(w, request.Message, launchErr)
					return
				}
			}
			session, location, launchErr := locateLaunchedSession(ctx, launcher, adapter, launchResult)
			if launchErr != nil {
				pend(launchResult.Location, fmt.Sprintf("discovery failed: %s", launchErr))
				return
			}
			if session == nil {
				if location == nil {
					d.writeAcceptedLaunch(w, request.Launcher, nil, "location is unresolved", untracked(alias))
					return
				}
				pend(location, "")
				return
			}
			if session.Surface == "" {
				session.Surface = surface.SurfaceKind(request.Surface)
				session.Cwd = cwd
				session.Name = request.Name
			}
			session.Runtime = &surface.Runtime{Launcher: request.Launcher, Location: location, Focusable: location != nil}
			if err := d.Registry.RegisterSession(*session); err != nil {
				d.writeAcceptedLaunch(w, request.Launcher, location, fmt.Sprintf("register session: %s", err), untracked(alias))
				return
			}
			if err := d.Registry.BindReservedAlias(reservation, session.ID); err != nil {
				d.writeAcceptedLaunch(w, request.Launcher, location, fmt.Sprintf("persist session name: %s", err), unkeptName(alias))
				return
			}
			if err := d.Registry.RecordSessionOrigin(registry.OriginOf(session.ID, creator)); err != nil {
				d.writeAcceptedLaunch(w, request.Launcher, location, fmt.Sprintf("record who started the session: %s", err), "Agenthail couldn't record who started it.")
				return
			}
			launchFields := map[string]any{"launcher": request.Launcher, "location": location, "sessionId": session.ID}
			if launchResult.Session != nil {
				d.writeStartedSessionOutcome(w, session, creator, alias, messageUUID, request.Message, launchResult.Sent, startErr, launchFields)
				return
			}
			launchFields["ok"] = true
			writeDashboardJSON(w, http.StatusCreated, launchFields)
			return
		}
		adapter := d.adapters().ForSurface(surface.SurfaceKind(request.Surface))
		starter, ok := adapter.(surface.SessionStarter)
		if adapter == nil || !ok {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this surface cannot start conversations")
			return
		}
		if strings.TrimSpace(request.Message) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "message is required")
			return
		}
		cwd, cwdErr := dashboardStartCwd(request.Cwd)
		if cwdErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", cwdErr.Error())
			return
		}
		alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
		reservation, reserved := d.reserveStartName(w, alias)
		if !reserved {
			return
		}
		defer func() { _ = d.Registry.ReleaseAliasReservation(reservation) }()
		ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
		defer cancel()
		messageUUID := uuid.NewString()
		ctx = surface.WithMessageUUID(ctx, messageUUID)
		session, sent, startErr := starter.StartSession(ctx, startOptions(cwd))
		if session != nil {
			session.Runtime = &surface.Runtime{Launcher: defaultLauncherForSurface(surface.SurfaceKind(request.Surface)), Focusable: false}
			if registerErr := d.Registry.RegisterSession(*session); registerErr != nil {
				writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but local registration failed: %s; do not retry automatically", registerErr), nil)
				return
			}
			if aliasErr := d.Registry.BindReservedAlias(reservation, session.ID); aliasErr != nil {
				writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but its name could not be persisted: %s; do not retry automatically", aliasErr), nil)
				return
			}
			if originErr := d.Registry.RecordSessionOrigin(registry.OriginOf(session.ID, creator)); originErr != nil {
				writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but who started it could not be recorded: %s; do not retry automatically", originErr), nil)
				return
			}
			d.writeStartedSessionOutcome(w, session, creator, alias, messageUUID, request.Message, sent, startErr, nil)
			return
		}
		if startErr == nil {
			startErr = errors.New("session starter returned no session")
		}
		d.writeUnstartedSession(w, request.Message, startErr)
		return
	}
	if request.Action == "notion-create" {
		name, reserved := d.reserveStartName(w, strings.TrimPrefix(strings.TrimSpace(request.Alias), "@"))
		if !reserved {
			return
		}
		defer func() { _ = d.Registry.ReleaseAliasReservation(name) }()
		ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
		defer cancel()
		receipt, createErr := d.createNotionThread(ctx, request.Message, name, request.Model)
		if createErr != nil {
			writeAPIError(w, http.StatusBadGateway, "bad_gateway", createErr.Error())
			return
		}
		writeDashboardJSON(w, http.StatusCreated, map[string]any{"ok": true, "result": receipt, "sessionId": receipt.SessionID})
		return
	}
	if request.Action == "queue-retry" {
		if request.QueueID <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "queueId is required")
			return
		}
		item, itemErr := d.Registry.QueueItem(request.QueueID, d.adapters())
		if itemErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", itemErr.Error())
			return
		}
		target, targetErr := d.Registry.Session(item.SessionID)
		if targetErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", targetErr.Error())
			return
		}
		// A paired host's session is retried by replaying the held request
		// with its original key; the host decides whether it can take it.
		if owner, ownerErr := d.Registry.SessionHost(target.ID); ownerErr != nil || owner == "" {
			adapter, adapterErr := d.adapters().For(target)
			if adapterErr != nil {
				writeAPIError(w, http.StatusConflict, "conflict", "target surface is not configured")
				return
			}
			if err := d.ensureDashboardWritable(r.Context(), adapter, target); err != nil {
				writeAPIError(w, http.StatusConflict, "conflict", err.Error())
				return
			}
		}
		if err := d.Registry.RetryMessage(request.QueueID); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "queue-cancel" {
		if request.QueueID <= 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "queueId is required")
			return
		}
		if err := d.Registry.CancelMessage(request.QueueID); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "channel-create" {
		if strings.TrimSpace(request.Channel) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "channel name is required")
			return
		}
		name := strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")
		if err := broadcast.ValidateChannelName(d.Registry, name); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if _, err := d.Registry.CreateChannel(name); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "channel-delete" {
		if err := d.Registry.DeleteChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "channel-send" {
		if strings.TrimSpace(request.Channel) == "" || strings.TrimSpace(request.Message) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "channel and message are required")
			return
		}
		target, err := broadcast.Resolve(d.Registry, request.Channel)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		operationCtx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
		defer cancel()
		if request.Author != "" && request.Author != surface.AuthorPerson || request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "author must be \"person\" and name no source session when set")
			return
		}
		service := d.coordinator()
		source, label := request.SourceSessionID, "the Agenthail CLI"
		switch {
		case source != "":
			label = d.resolveDisplay(service.Canonical(source))
		case request.Author == surface.AuthorPerson:
			source = surface.PersonSessionID
			if label, err = d.Registry.PersonName(); err != nil {
				writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
				return
			}
		}
		broadcaster := broadcast.Broadcaster{Registry: d.Registry, Coordinators: service, Delivery: daemonBroadcastDelivery{d: d, sender: coordinator.Sender{Service: service}}}
		result, err := broadcaster.Send(operationCtx, target, request.Message, label, source)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		sent, queued, submitted, failed := result.Count(broadcast.OutcomeSent), result.Count(broadcast.OutcomeQueued), result.Count(broadcast.OutcomeSubmitted), result.Count(broadcast.OutcomeFailed)
		if failed > 0 {
			outcomes := make([]string, 0, len(result.Recipients))
			for _, recipient := range result.Recipients {
				label := recipient.Coordinator
				if label == "" {
					label = d.resolveDisplay(recipient.SessionID)
				}
				outcome := label + " " + recipient.Outcome
				if recipient.Outcome == broadcast.OutcomeFailed && recipient.Detail != "" {
					outcome += ": " + recipient.Detail
				}
				outcomes = append(outcomes, outcome)
			}
			writeAPIError(w, http.StatusConflict, "conflict", fmt.Sprintf("channel delivery: %d sent, %d queued, %d submitted, %d failed (%s)", sent, queued, submitted, failed, strings.Join(outcomes, "; ")))
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "target": result.Target, "recipients": result.Recipients, "sent": sent, "queued": queued, "submitted": submitted, "failed": failed})
		return
	}
	if request.Action == "channel-rename" {
		name := strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")
		newName := strings.TrimPrefix(strings.TrimSpace(request.NewChannel), "#")
		if err := broadcast.ValidateChannelName(d.Registry, newName); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if err := d.Registry.RenameChannel(name, newName); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "channel-add" || request.Action == "channel-remove" {
		if request.TargetID == "" || strings.TrimSpace(request.Channel) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "channel and targetId are required")
			return
		}
		targetID, resolveErr := d.resolveLinkTarget(request.TargetID)
		if resolveErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("resolve agent: %s", resolveErr))
			return
		}
		var actionErr error
		if request.Action == "channel-add" {
			target, targetErr := d.Registry.Session(targetID)
			if targetErr != nil {
				writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
				return
			}
			if !d.hostOwned(target.ID) {
				adapter, adapterErr := d.adapters().For(target)
				if adapterErr != nil {
					writeAPIError(w, http.StatusConflict, "conflict", "target surface is not configured")
					return
				}
				if err := d.ensureDashboardWritable(r.Context(), adapter, target); err != nil {
					writeAPIError(w, http.StatusConflict, "conflict", err.Error())
					return
				}
			}
			actionErr = d.Registry.AddToChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#"), targetID)
		} else {
			actionErr = d.Registry.RemoveFromChannel(strings.TrimPrefix(strings.TrimSpace(request.Channel), "#"), targetID)
		}
		if actionErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", actionErr.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "relay-add" {
		if request.FromID == "" || request.ToID == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "fromId and toId are required")
			return
		}
		fromID, fromErr := d.Registry.ResolveTarget(request.FromID)
		if fromErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("resolve source agent: %s", fromErr))
			return
		}
		toID, toErr := d.resolveLinkTarget(request.ToID)
		if toErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("resolve destination agent: %s", toErr))
			return
		}
		toSession, toSessionErr := d.Registry.Session(toID)
		if toSessionErr != nil {
			writeAPIError(w, http.StatusNotFound, "not_found", "destination session not found")
			return
		}
		if !d.hostOwned(toSession.ID) {
			adapter, adapterErr := d.adapters().For(toSession)
			if adapterErr != nil {
				writeAPIError(w, http.StatusConflict, "conflict", "destination surface is not configured")
				return
			}
			if err := d.ensureDashboardWritable(r.Context(), adapter, toSession); err != nil {
				writeAPIError(w, http.StatusConflict, "conflict", err.Error())
				return
			}
		}
		pattern := request.Pattern
		if pattern == "" {
			pattern = ".*"
		}
		fromSession, err := d.Registry.Session(fromID)
		if err != nil {
			writeAPIError(w, http.StatusNotFound, "not_found", "source session not found")
			return
		}
		fromAdapter, fromAdapterErr := d.adapters().For(fromSession)
		if fromAdapterErr != nil {
			writeAPIError(w, http.StatusConflict, "conflict", "source surface is not configured")
			return
		}
		if err := d.Registry.PrepareRelaySource(r.Context(), fromAdapter, fromSession, request.Handoff); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if _, err := d.Registry.AddRelayRoute(fromID, toID, pattern, request.Once, request.Handoff); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "relay-remove" {
		if err := d.Registry.RemoveRoute(request.RelayID); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.Action == "codex-launch" {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("find Agenthail executable: %s", executableErr))
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
			writeAPIError(w, http.StatusBadGateway, "bad_gateway", detail)
			return
		}
		if adapter := d.adapters().ForSurface(surface.KindCodex); adapter != nil {
			d.recordSurfaceHealth(ctx, adapter, nil)
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": strings.TrimSpace(string(output))})
		return
	}
	if request.Action == "runtime-ensure" {
		adapter := d.adapters().ForSurface(surface.KindCodex)
		ensurer, ok := adapter.(surface.RuntimeEnsurer)
		if adapter == nil || !ok {
			writeAPIError(w, http.StatusConflict, "conflict", "Codex managed runtime cannot be repaired here")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		if err := ensurer.EnsureRuntime(ctx); err != nil {
			writeAPIError(w, http.StatusBadGateway, "bad_gateway", err.Error())
			return
		}
		d.recordSurfaceHealth(ctx, adapter, nil)
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if request.SessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "sessionId is required")
		return
	}
	session, err := d.Registry.Session(request.SessionID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if request.Action == "limit-resume" || request.Action == "limit-resume-cancel" {
		d.limitResumeAction(w, session.ID, request.Action == "limit-resume")
		return
	}
	if request.Action == "session-focus" {
		if session.Runtime == nil || !session.Runtime.Focusable || session.Runtime.Location == nil {
			writeAPIError(w, http.StatusConflict, "conflict", "session is not focusable")
			return
		}
		if d.transportResolver == nil {
			writeAPIError(w, http.StatusConflict, "conflict", "launcher transport is unavailable")
			return
		}
		launcher, ok := d.transportResolver.Launcher(session.Runtime.Launcher)
		focuser, focusable := launcher.(surface.Focuser)
		if !ok || !focusable {
			writeAPIError(w, http.StatusConflict, "conflict", "launcher cannot focus sessions")
			return
		}
		adapter, adapterErr := d.adapters().For(session)
		if adapterErr != nil {
			session.Runtime.Focusable = false
			_ = d.Registry.SaveSessionRuntime(session.ID, session.Runtime)
			writeAPIError(w, http.StatusConflict, "conflict", "session surface is unavailable")
			return
		}
		located, err := adapter.List(r.Context())
		if err != nil {
			session.Runtime.Focusable = false
			_ = d.Registry.SaveSessionRuntime(session.ID, session.Runtime)
			writeAPIError(w, http.StatusConflict, "conflict", "session location is unavailable")
			return
		}
		locations := launcher.Locate(r.Context(), located)
		current, found := locations[session.ID]
		if !found || current != *session.Runtime.Location {
			session.Runtime.Focusable = false
			_ = d.Registry.SaveSessionRuntime(session.ID, session.Runtime)
			writeAPIError(w, http.StatusConflict, "conflict", "session location is stale")
			return
		}
		if err := focuser.Focus(r.Context(), *session.Runtime.Location); err != nil {
			writeAPIError(w, http.StatusBadGateway, "bad_gateway", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "launcher": session.Runtime.Launcher, "location": session.Runtime.Location, "sessionId": session.ID})
		return
	}
	if request.Action == "session-archive" || request.Action == "session-unarchive" {
		d.sessionArchiveAction(w, request.Action, session.ID)
		return
	}
	if request.Action == "alias" {
		alias := strings.TrimPrefix(strings.TrimSpace(request.Alias), "@")
		if alias == "" || strings.ContainsAny(alias, " \t\r\n/#") || len(alias) > 80 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "name must be 1 to 80 characters without spaces, /, or #")
			return
		}
		if err := d.Registry.SetAlias(alias, session.ID); err != nil {
			var taken registry.AliasTakenError
			var reserved registry.AliasReservedError
			if errors.As(err, &taken) {
				writeAPIError(w, http.StatusConflict, "conflict", fmt.Sprintf("@%s is already used by another session", alias))
				return
			}
			if errors.As(err, &reserved) {
				writeAPIError(w, http.StatusConflict, "conflict", reserved.Error())
				return
			}
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("name conversation: %s", err))
			return
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "identified", SessionID: session.ID, Result: "@" + alias})
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "alias": alias})
		return
	}
	adapter, transportErr := d.adapters().For(session)
	if transportErr != nil {
		writeAPIError(w, http.StatusConflict, "conflict", transportErr.Error())
		return
	}
	if request.PID != 0 {
		if err := pinHolder(r.Context(), adapter, session, request.PID, request.PIDStartedAt); err != nil {
			writeAPIError(w, http.StatusConflict, "conflict", err.Error())
			return
		}
	}
	if request.Action == "session-adopt" {
		d.dashboardSessionAdopt(w, r, adapter, session, surface.AdoptRequest{StopTerminal: request.StopTerminal})
		return
	}
	if request.Action == "session-fork" || request.Action == "native-queue" || strings.HasPrefix(request.Action, "session-lifecycle-") {
		d.dashboardSessionOperation(w, r, adapter, session, request.Action, request.Fork, strings.TrimPrefix(strings.TrimSpace(request.Alias), "@"), strings.TrimSpace(request.SourceSessionID), request.NativeQueue)
		return
	}
	if err := d.ensureDashboardWritable(r.Context(), adapter, session); err != nil {
		writeAPIError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	effective := surface.EffectiveCapabilities(adapter, session)
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	var result any
	switch request.Action {
	case "send":
		if strings.TrimSpace(request.Message) == "" && len(request.Attachments) == 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "message is required")
			return
		}
		attached, uploadErr := d.resolveUploads(request.Attachments)
		if uploadErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", uploadErr.Error())
			return
		}
		if len(attached) > 0 && !effective.Attach {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session cannot take attached files")
			return
		}
		if request.Author != "" && request.Author != surface.AuthorPerson {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "author must be \"person\" when set")
			return
		}
		if request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "a person's message cannot also name a source session")
			return
		}
		if request.SourceSessionID != "" {
			if _, err := d.Registry.Session(request.SourceSessionID); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "source session not found")
				return
			}
		}
		if request.RelayHops != 0 {
			if len(attached) > 0 {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "a relay leg carries no attached files")
				return
			}
			d.queueHostRelay(w, r, session, request.Message, request.SourceSessionID, request.BusyDelivery, request.RelayHops)
			return
		}
		options := surface.SendOptions{Model: request.Model, SourceSessionID: request.SourceSessionID, BusyDelivery: request.BusyDelivery, TurnOptions: request.TurnOptions, Author: request.Author, PersonName: request.PersonName, Uploads: attached}
		receipt, actionErr := coordinator.Sender{Service: d.coordinator()}.DeliverWithOptions(ctx, adapter, session, request.Message, "", options)
		if actionErr != nil {
			writeAPIError(w, http.StatusBadGateway, "bad_gateway", actionErr.Error())
			return
		}
		result = receipt
	case "steer":
		if !effective.Steer || strings.TrimSpace(request.Message) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session cannot be steered")
			return
		}
		if request.Author != "" && request.Author != surface.AuthorPerson {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "author must be \"person\" when set")
			return
		}
		if request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "a person's message cannot also name a source session")
			return
		}
		if request.SourceSessionID != "" {
			if _, sourceErr := d.Registry.Session(request.SourceSessionID); sourceErr != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "source session not found")
				return
			}
			ctx = surface.WithSourceSessionID(ctx, request.SourceSessionID)
		}
		if request.PersonName != "" {
			ctx = surface.WithPersonName(ctx, request.PersonName)
		}
		result, err = d.dispatcher().Steer(ctx, adapter, session, request.Message, request.Author)
	case "interrupt":
		if !effective.Interrupt {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session cannot be interrupted")
			return
		}
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: "interrupt"})
	case "compact":
		if !effective.Compact {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session cannot be compacted")
			return
		}
		result, err = d.dispatcher().Compact(ctx, adapter, session)
	case "goal-set", "goal-edit":
		result, err = d.dispatcher().GoalSet(ctx, adapter, session, request.Message, request.Action == "goal-edit")
	case "goal-clear":
		result, err = d.dispatcher().GoalClear(ctx, adapter, session)
	case "goal-pause", "goal-resume", "goal-budget":
		controller, ok := adapter.(surface.GoalController)
		if !effective.Goal || !ok {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session does not support typed goal controls")
			return
		}
		var update surface.GoalUpdate
		switch request.Action {
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
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "token budget must be a non-negative integer")
				return
			}
			update = surface.GoalUpdate{TokenBudget: &value}
		}
		err = controller.UpdateGoal(ctx, session, update)
	case "model":
		if !effective.Model {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session does not support model switching")
			return
		}
		if strings.TrimSpace(request.Model) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "model is required")
			return
		}
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: "model", Value: request.Model})
		if err == nil {
			d.recordSetting(session, "model", strings.TrimSpace(request.Model), actionActor(r))
		}
		if err == nil && session.Surface == surface.KindClaude {
			if persistErr := d.Registry.RegisterSession(*session); persistErr != nil {
				err = fmt.Errorf("persist Claude model state: %w", persistErr)
			}
		}
	case "permission-mode":
		if !effective.PermissionMode {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session's permission mode cannot change while it runs")
			return
		}
		mode := strings.TrimSpace(request.PermissionMode)
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: "permission-mode", Value: mode})
		if err == nil {
			d.recordSetting(session, "permission-mode", mode, actionActor(r))
		}
	case "respond":
		d.respond(ctx, w, adapter, session, strings.TrimSpace(request.RequestID), request.Decision, actionActor(r))
		return
	case "context", "usage", "status", "effort", "rename":
		if _, ok := adapter.(surface.Controller); !ok || !effective.Commands {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "this session does not support Claude Remote Control commands")
			return
		}
		result, err = d.control(ctx, adapter, session, surface.ControlRequest{Action: request.Action, Value: request.Message})
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "unsupported dashboard action")
		return
	}
	if err != nil {
		if control, ok := result.(*surface.ControlResult); ok && control != nil {
			writeAPIErrorWith(w, http.StatusBadGateway, "control_failed", err.Error(), map[string]any{"result": control})
			return
		}
		if surface.DeliveryTerminalReason(err) == surface.DeliveryInvalidRequest {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		writeAPIError(w, http.StatusBadGateway, "bad_gateway", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
}

// control runs a model or interrupt change, or an allowlisted Claude command,
// and records its request id. A surface without request-linked controls
// confirms the change when its call returns.
func (d *Daemon) control(ctx context.Context, adapter surface.Surface, session *surface.Session, request surface.ControlRequest) (*surface.ControlResult, error) {
	if controller, ok := adapter.(surface.Controller); ok {
		result, err := controller.Control(ctx, session, request)
		d.dispatcher().RecordControl(result, err)
		return result, err
	}
	result := &surface.ControlResult{SessionID: session.ID, Action: request.Action, Evidence: surface.EvidenceDelivered, Source: string(adapter.Name())}
	var err error
	if request.Action == "interrupt" {
		err = adapter.Interrupt(ctx, session)
	} else if setter, ok := adapter.(surface.ModelSetter); ok && request.Value != "" {
		var change surface.ModelChange
		change, err = setter.SetModel(ctx, session, request.Value)
		result.Output, result.Change = change.Text(), &change
	} else {
		result.Output, err = adapter.Model(ctx, session, request.Value)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
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
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if r.URL.Query().Get("surface") != "codex" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "only Codex history search is available")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) < 3 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "search requires at least 3 characters")
		return
	}
	adapter := d.adapters().ForSurface(surface.KindCodex)
	searcher, ok := adapter.(surface.SessionSearcher)
	if !ok {
		writeAPIError(w, http.StatusNotImplemented, "request_failed", "Codex history search is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	stored, err := d.Registry.SearchSessions(surface.KindCodex, query, 20)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("search saved Codex conversations: %s", err))
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
			writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("store search result: %s", err))
			return
		}
		alias, _ := d.Registry.ReverseAlias(result.Session.ID)
		effective := surface.EffectiveCapabilities(adapter, &result.Session)
		payload = append(payload, map[string]any{
			"session": dashboardSession{ID: result.Session.ID, Surface: result.Session.Surface, Name: result.Session.Name, Cwd: result.Session.Cwd, Alias: alias, Status: result.Session.Status, WaitingOn: result.Session.WaitingOn, LastActive: result.Session.LastActive, Capabilities: effective.Capabilities, ReadOnly: effective.ReadOnly, ReadOnlyReason: effective.ReadOnlyReason, Source: result.Session.Source, Transport: result.Session.Transport, Subagent: result.Session.Subagent, Delegated: result.Session.Delegated()},
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
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	sessionID := r.URL.Query().Get("id")
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "session id is required")
		return
	}
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		if aliased, aliasErr := d.Registry.LookupAlias(strings.TrimPrefix(sessionID, "@")); aliasErr == nil {
			session, err = d.Registry.Session(aliased)
		}
	}
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	adapter, adapterErr := d.adapters().For(session)
	if adapterErr != nil {
		writeAPIError(w, http.StatusConflict, "conflict", "surface is not configured")
		return
	}
	limit := 10
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil || parsed < 1 || parsed > surface.MaxExchangePage {
			writeAPIError(w, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("Use a limit between 1 and %d exchanges.", surface.MaxExchangePage))
			return
		}
		limit = parsed
	}
	alias, _ := d.Registry.ReverseAlias(session.ID)
	effective := surface.EffectiveCapabilities(adapter, session)
	var timelineBefore int64
	if raw := r.URL.Query().Get("timelineBefore"); raw != "" {
		var parseErr error
		timelineBefore, parseErr = strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || timelineBefore < 0 {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid activity cursor")
			return
		}
	}
	var sessionRead *surface.SessionReadResult
	var sessionReadErr error
	if historyBefore, provider := decodeProviderHistoryCursor(timelineBefore); provider {
		historyCtx, cancel := context.WithTimeout(r.Context(), operationTimeout)
		sessionRead, sessionReadErr = d.readProviderHistoryPage(historyCtx, session, adapter, historyBefore, limit)
		cancel()
	} else if timelineBefore > 0 {
		sessionRead, sessionReadErr = d.readJournalPage(session.ID, uint64(timelineBefore), limit)
	} else {
		// What the source catches up after the wait arrives on the session
		// stream after the page's seq.
		seedCtx, cancel := context.WithTimeout(r.Context(), min(operationTimeout, sessionPageCatchUpWait))
		seedErr := d.sources.seed(seedCtx, session, adapter)
		cancel()
		sessionRead, sessionReadErr = d.readJournalPage(session.ID, 0, limit)
		if sessionReadErr == nil && seedErr != nil && sessionRead.JournalSeq == 0 {
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
			"earliestPosition": historyGap.Earliest,
			"latestPosition":   historyGap.Latest,
		})
		return
	}
	exchanges, transcript := truncateSessionExchanges(sessionRead.Exchanges)
	response := map[string]any{"session": session, "alias": alias, "limitResume": d.limitResumeRow(session.ID), "exchanges": exchanges, "capabilities": effective.Capabilities, "readOnly": effective.ReadOnly, "readOnlyReason": effective.ReadOnlyReason, "readSource": sessionRead.Source, "transcriptTruncated": transcript.Truncated || sessionRead.Truncated, "transcriptOriginalBytes": transcript.OriginalBytes, "transcriptReturnedBytes": transcript.ReturnedBytes, "transcriptOriginalExchanges": transcript.OriginalExchanges, "transcriptReturnedExchanges": len(exchanges)}
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
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	session, err := d.Registry.Session(r.URL.Query().Get("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	adapter, adapterErr := d.adapters().For(session)
	if adapterErr != nil {
		writeAPIError(w, http.StatusConflict, "conflict", "surface is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	effective := surface.EffectiveCapabilities(adapter, session)
	requests := map[string]func() (any, error){}
	if provider, ok := adapter.(surface.ContextUsageProvider); ok {
		requests["context"] = func() (any, error) { return provider.ContextUsage(ctx, session) }
	}
	if effective.GoalStatus {
		requests["goal"] = func() (any, error) { return adapter.GoalGet(ctx, session) }
	}
	if effective.Model {
		requests["model"] = func() (any, error) { return adapter.Model(ctx, session, "") }
		if reader, ok := adapter.(surface.PendingModelReader); ok {
			requests["pendingModel"] = func() (any, error) { return reader.PendingModel(session.ID), nil }
		}
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
		allSubagents := r.URL.Query().Get("subagents") == "all"
		requests["claudeSubagents"] = func() (any, error) {
			links, err := observer.ObserveClaudeSubagents(ctx, session)
			if err != nil {
				return nil, err
			}
			return newClaudeSubagentPage(links, allSubagents), nil
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
			} else if page, ok := result.value.(claudeSubagentPage); ok {
				response[result.field] = page.links
				response["claudeSubagentsTotal"] = page.total
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
	// Every exchange of the page is returned, so the page cursor never skips
	// one; the newest take what older ones leave of the budget.
	remaining := sessionTranscriptJSONBudget
	result := make([]surface.Exchange, len(exchanges))
	for index, exchange := range exchanges {
		share := remaining / (len(exchanges) - index)
		userBudget := min(sessionTranscriptFieldLimit, share)
		if exchange.User != "" && exchange.Assistant != "" {
			userBudget = min(userBudget, share/2)
		}
		user, userEncoded, userTruncated := truncateJSONText(exchange.User, userBudget)
		assistant, assistantEncoded, assistantTruncated := truncateJSONText(exchange.Assistant, min(sessionTranscriptFieldLimit, share-userEncoded))
		remaining -= userEncoded + assistantEncoded
		exchange.User = user
		exchange.Assistant = assistant
		metadata.ReturnedBytes += len(user) + len(assistant)
		metadata.Truncated = metadata.Truncated || userTruncated || assistantTruncated
		result[index] = exchange
	}
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
	encoded, err := marshalAPIJSON(value)
	if err == nil {
		_, _ = w.Write(append(encoded, '\n'))
	}
}

// writeUnstartedSession records and reports a session-create that returned
// no session.
func (d *Daemon) writeUnstartedSession(w http.ResponseWriter, message string, startErr error) {
	unknown := surface.IsDeliveryOutcomeUnknown(startErr)
	status, code := "failed", "create_failed"
	if unknown {
		status, code = "unknown", "create_unknown"
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: status, Message: message, Error: startErr.Error()})
	detail := startErr.Error()
	if unknown {
		detail = fmt.Sprintf("initial turn outcome is ambiguous, but no session identity was returned; inspect the provider before any explicit retry: %s", startErr)
	}
	writeAPIError(w, http.StatusBadGateway, code, detail)
}

func (d *Daemon) writeStartedSessionOutcome(w http.ResponseWriter, session *surface.Session, creator, alias, messageUUID, message string, sent *surface.SendResult, startErr error, extra map[string]any) {
	if startErr != nil {
		intent, intentErr := d.Registry.RecordSessionCreationIntent(session.ID, surface.SendOptions{SourceSessionID: creator, Author: surface.AuthorPerson}.Sender(), messageUUID, message)
		if intentErr != nil {
			_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: fmt.Sprintf("%s; durable delivery intent failed: %s", startErr, intentErr)})
			writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but its delivery intent could not be persisted: %s; do not retry automatically", intentErr), extra)
			return
		}
		if surface.IsDeliveryOutcomeUnknown(startErr) {
			_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "submitted", SessionID: session.ID, Message: message, Error: startErr.Error()})
			writeSubmittedSession(w, session, intent.ID, submittedSessionDetail(session, alias), extra)
			return
		}
		_, noticeErr := d.Registry.FailDeliveryIntentWithNotice(intent.ID, startErr.Error())
		if noticeErr != nil {
			d.log.Printf("queue initial delivery failure notice for %s: %s", session.ID, noticeErr)
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: startErr.Error()})
		if err := d.catalog.flushCommitted(); err != nil {
			d.log.Printf("publish initial delivery failure: %s", err)
		}
		writeInitialSessionFailure(w, session, intent.ID, startErr.Error(), extra)
		return
	}
	result := ""
	if sent != nil {
		result = sent.UUID
	}
	if result != "" {
		if runtimeErr := d.Registry.MarkDeliveryStarted(session.ID, result, ""); runtimeErr != nil {
			_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, Message: message, Result: result, Error: runtimeErr.Error()})
		}
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "sent", SessionID: session.ID, Message: message, Result: result})
	writeSessionCreateJSON(w, http.StatusCreated, map[string]any{"ok": true, "session": session, "result": sent}, extra)
}

func writeSessionCreateJSON(w http.ResponseWriter, status int, body, extra map[string]any) {
	for key, value := range extra {
		body[key] = value
	}
	writeDashboardJSON(w, status, body)
}

func writeSubmittedSession(w http.ResponseWriter, session *surface.Session, deliveryID int64, detail string, extra map[string]any) {
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
	writeSessionCreateJSON(w, http.StatusAccepted, body, extra)
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

func writeInitialSessionFailure(w http.ResponseWriter, session *surface.Session, deliveryID int64, message string, extra map[string]any) {
	body := map[string]any{"session": session, "deliveryId": deliveryID}
	for key, value := range extra {
		body[key] = value
	}
	writeAPIErrorWith(w, http.StatusBadGateway, "initial_turn_failed", message, body)
}

func writeCreateStorageFailure(w http.ResponseWriter, session *surface.Session, warning string, extra map[string]any) {
	body := map[string]any{"session": session}
	for key, value := range extra {
		body[key] = value
	}
	writeAPIErrorWith(w, http.StatusInternalServerError, "create_storage_failed", warning, body)
}

// writeAcceptedLaunch answers a launch that may be running without a
// registered session; notice tells the person what Agenthail could not keep.
func (d *Daemon) writeAcceptedLaunch(w http.ResponseWriter, launcher string, location *surface.Location, detail, notice string) {
	d.log.Printf("accepted %s launch: %s", launcher, detail)
	warning := fmt.Sprintf("Submitted to %s.", launcher)
	if notice != "" {
		warning += " " + notice
	}
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

// untracked is the notice for a launch Agenthail holds no record of; the
// session is registered only when discovery finds it.
func untracked(alias string) string {
	return strings.TrimSpace("It appears in the sidebar once Agenthail finds it. " + unkeptName(alias))
}

func unkeptName(alias string) string {
	if alias == "" {
		return ""
	}
	return fmt.Sprintf("Its name @%s wasn't kept; name it once it appears in the sidebar.", alias)
}

// reserveStartName holds a new session's name from before it starts until it
// is registered, refusing a name another session, coordinator or start holds.
func (d *Daemon) reserveStartName(w http.ResponseWriter, name string) (registry.AliasReservation, bool) {
	reservation, err := d.Registry.ReserveAlias(name, time.Now().Add(surfaceOperationTimeout+startNameMargin))
	if err == nil {
		return reservation, true
	}
	if registry.AliasRefused(err) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
	} else {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", fmt.Sprintf("reserve conversation name: %s", err))
	}
	return registry.AliasReservation{}, false
}

// pinHolder addresses session to the one live process pid among those that
// hold its conversation, as a target naming that process does.
// pinHolder addresses session to the one process pid among those that hold
// its conversation. startedAt, when the picker sent it, is that process's
// start as listed, so a pid reused since the pick does not match.
func pinHolder(ctx context.Context, adapter surface.Surface, session *surface.Session, pid int, startedAt int64) error {
	if session.Surface != surface.KindClaude || pid < 0 {
		return fmt.Errorf("pid names a Claude process that holds the conversation")
	}
	pinned, err := adapter.Resolve(ctx, strconv.Itoa(pid))
	if err != nil || pinned.ID != session.ID || pinned.PID != pid || startedAt != 0 && pinned.StartedAt.UnixMilli() != startedAt {
		return fmt.Errorf("Claude process %d does not hold conversation %s", pid, session.ID)
	}
	session.PID, session.HolderPID, session.HolderStart, session.Transport, session.Bridge = pinned.PID, pinned.HolderPID, pinned.HolderStart, pinned.Transport, pinned.Bridge
	return nil
}
