package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/uiconf"
	"github.com/zm2231/agenthail/internal/voice"
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
	mux.Handle("/js/", dashboardModuleHandler())
	mux.Handle("/css/", dashboardStylesHandler())
	mux.HandleFunc("/tokens.css", dashboard.asset("text/css; charset=utf-8", dashboardCSS))
	mux.HandleFunc("/palette.css", dashboard.asset("text/css; charset=utf-8", []byte(uiconf.PaletteCSS())))
	mux.HandleFunc("/logo.png", dashboard.asset("image/png", dashboardLogo))
	mux.HandleFunc("/favicon.png", dashboard.asset("image/png", dashboardFavicon))
	mux.HandleFunc("/favicon.ico", dashboard.asset("image/png", dashboardFavicon))
	mux.HandleFunc("/api/state", dashboard.guard(func(w http.ResponseWriter, r *http.Request) { d.dashboardStateCached(dashboard, w, r) }))
	mux.HandleFunc("/api/session", dashboard.guard(d.dashboardBrowserSession))
	mux.HandleFunc("/api/session-metadata", dashboard.guard(d.dashboardBrowserSessionMetadata))
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
