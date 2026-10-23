package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/buildinfo"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/uploads"
)

const (
	defaultPollInterval      = 5 * time.Second
	catalogDiscoveryInterval = 30 * time.Second
	catalogStatusInterval    = time.Second
	// deliveryProblemInterval is how often settled delivery failures leave
	// the session rows.
	deliveryProblemInterval = 10 * time.Second
	// pendingLaunchLifetime holds a launched session's name while discovery
	// looks for it, once per catalogDiscoveryInterval.
	pendingLaunchLifetime = 5 * time.Minute
	// pendingLaunchRetention keeps a launch past its name's lifetime, so a
	// session found late still takes the name if no one else has.
	pendingLaunchRetention = 24 * time.Hour
	startNameMargin        = 30 * time.Second
)

type Daemon struct {
	Registry             *registry.Registry
	sessionFiles         sessionFileLists
	claudeConfigRootsMu  sync.Mutex
	claudeConfigRootsAt  time.Time
	claudeConfigRootList []surface.ClaudeConfigRoot
	coordinatorMu        sync.Mutex
	pollInterval         time.Duration
	Surfaces             []surface.Surface
	log                  *log.Logger
	errorMu              sync.Mutex
	observeErrors        map[string]observedError
	ui                   *uiState
	snapshots            *snapshotRelay
	retryMu              sync.Mutex
	observeRetry         map[string]observeRetry
	notificationMu       sync.Mutex
	notificationArmed    map[string]bool
	waitingLocks         sync.Map
	announceMu           sync.Mutex
	announcing           map[string]string
	announcingRefusal    map[int64]int64
	catalogNudge         chan struct{}
	presence             catalogPresence
	observerNudge        chan struct{}
	// catalogSelected, when set, runs after a catalog publication has chosen
	// what a session waits on and before its row commits.
	catalogSelected func(sessionID string)
	// waitingContended, when set, runs when a session's waiting lock is held
	// by another caller and this one is about to wait for it.
	waitingContended func(sessionID string)
	// notifyDesktop shows a desktop notification; it is NotifyRequest outside
	// tests.
	notifyDesktop func(title, message, sessionID, identifier, requestID string, actions []surface.RequestAction) error
	// withdrawDesktop removes a desktop notification; it is
	// WithdrawNotification outside tests.
	withdrawDesktop    func(identifier string) error
	background         *backgroundWork
	browserWithdrawals browserWithdrawals
	events             *eventHub
	catalog            *catalogHub
	// catalogLiveMu guards catalogLive, which discovery replaces and the
	// status pass reads from its own goroutine. It is held only for map
	// reads and writes; a session's waiting lock orders its row and entry.
	catalogLiveMu     sync.Mutex
	catalogLive       map[string]*catalogLiveSession
	catalogIdentities *catalogIdentityCache
	// catalogProjectUnarchived is set by either catalog goroutine when a
	// session's activity unarchived its project.
	catalogProjectUnarchived atomic.Bool
	sources                  *sessionSourceManager
	sourceHoldMu             sync.Mutex
	surfaceHealthMu          sync.Mutex
	surfaceHealth            map[surface.SurfaceKind]dashboardSurface
	sourceHolds              map[string]map[string]func()
	dashboard                *dashboardServer
	transportResolver        *SessionTransportResolver
	claudeHost               *claudehost.Manager
	build                    buildinfo.Identity
	identityMu               sync.RWMutex
	identity                 registry.HostIdentity
	federation               *federation
	uploads                  *uploads.Store
	limitResumes             *limitResumer
	// limitResumeEnabled, when set, replaces the host's resume after limit
	// setting.
	limitResumeEnabled func() bool
	// limitResumesRead, when set, runs each time usage-limit stops are read.
	limitResumesRead func()
	// liveSources names the surface of each session the catalog holds a
	// source for because its process is live.
	liveSources map[string]surface.SurfaceKind
}

func (d *Daemon) SetLaunchers(launchers []surface.Launcher) {
	d.transportResolver = NewSessionTransportResolver(launchers)
}

type observedError struct {
	message string
	at      time.Time
}

type observeRetry struct {
	failures int
	retryAt  time.Time
}

func (d *Daemon) resolveDisplay(sessionID string) string {
	if d.Registry != nil {
		if alias, err := d.Registry.ReverseAlias(sessionID); err == nil && alias != "" {
			return "@" + alias
		}
		if sfc, name, _, err := d.Registry.GetSession(sessionID); err == nil {
			if name != "" {
				return sfc + "/" + truncate(name, 20)
			}
		}
	}
	return truncate(sessionID, 24)
}

func New(reg *registry.Registry, surfaces []surface.Surface) *Daemon {
	if reg != nil {
		reg.SetBusyPolicy(deliverypolicy.Load)
	}
	d := &Daemon{
		Registry:          reg,
		pollInterval:      defaultPollInterval,
		Surfaces:          surfaces,
		log:               log.New(os.Stderr, "[daemon] ", log.LstdFlags),
		observeErrors:     map[string]observedError{},
		observeRetry:      map[string]observeRetry{},
		notificationArmed: map[string]bool{},
		notifyDesktop:     NotifyRequest,
		withdrawDesktop:   WithdrawNotification,
		events:            newEventHub(reg),
		sources:           newSessionSourceManager(reg),
		sourceHolds:       map[string]map[string]func(){},
		liveSources:       map[string]surface.SurfaceKind{},
		catalogLive:       map[string]*catalogLiveSession{},
		catalogIdentities: newCatalogIdentityCache(),
		catalogNudge:      make(chan struct{}, 1),
		observerNudge:     make(chan struct{}, 1),
		federation:        newFederation(),
		uploads:           uploads.New(uploadsDir(reg)),
		background:        newBackgroundWork(),
		limitResumes:      newLimitResumer(),
	}
	d.ui = newUIState(reg)
	d.snapshots = newSnapshotRelay()
	identity, err := reg.HostIdentity()
	if err != nil {
		d.log.Printf("host identity: %s", err)
	}
	d.identity = identity
	d.transportResolver = NewSessionTransportResolver(surface.NewLaunchers(surfaces))
	kinds := make([]surface.SurfaceKind, 0, len(surfaces))
	for _, adapter := range surfaces {
		kinds = append(kinds, adapter.Name())
	}
	if err := reg.RetainCatalogSurfaces(kinds); err != nil {
		d.log.Printf("catalog surfaces: %s", err)
	}
	d.catalog = newCatalogHub(reg)
	d.catalogIdentities.changed = d.requestCatalogDiscovery
	if prompts := d.claudePrompts(); prompts != nil {
		prompts.OnPromptChange(d.claudePromptChanged)
	}
	return d
}

func (d *Daemon) observationAllowed(sessionID string) bool {
	d.retryMu.Lock()
	defer d.retryMu.Unlock()
	retry, found := d.observeRetry[sessionID]
	return !found || !time.Now().Before(retry.retryAt)
}

func (d *Daemon) recordObservationFailure(sessionID string) {
	d.retryMu.Lock()
	retry := d.observeRetry[sessionID]
	retry.failures++
	delay := 5 * time.Second * time.Duration(1<<min(retry.failures-1, 4))
	if delay > time.Minute {
		delay = time.Minute
	}
	retry.retryAt = time.Now().Add(delay)
	d.observeRetry[sessionID] = retry
	d.retryMu.Unlock()
}

func (d *Daemon) clearObservationFailure(sessionID string) {
	d.retryMu.Lock()
	delete(d.observeRetry, sessionID)
	d.retryMu.Unlock()
}

func (d *Daemon) pruneObservationFailures(watched []registry.WatchedSession) {
	active := make(map[string]struct{}, len(watched))
	for _, session := range watched {
		active[session.ID] = struct{}{}
	}
	d.retryMu.Lock()
	for sessionID := range d.observeRetry {
		if _, found := active[sessionID]; !found {
			delete(d.observeRetry, sessionID)
		}
	}
	d.retryMu.Unlock()
	d.notificationMu.Lock()
	for sessionID := range d.notificationArmed {
		if _, found := active[sessionID]; !found {
			delete(d.notificationArmed, sessionID)
		}
	}
	d.notificationMu.Unlock()
}

func (d *Daemon) logObserveError(sessionID string, err error) {
	now := time.Now()
	message := err.Error()
	d.errorMu.Lock()
	previous, found := d.observeErrors[sessionID]
	if found && previous.message == message && now.Sub(previous.at) < time.Minute {
		d.errorMu.Unlock()
		return
	}
	d.observeErrors[sessionID] = observedError{message: message, at: now}
	d.errorMu.Unlock()
	d.log.Printf("observe %s: %s", d.resolveDisplay(sessionID), err)
}

func (d *Daemon) clearObserveError(sessionID string) {
	d.errorMu.Lock()
	delete(d.observeErrors, sessionID)
	d.errorMu.Unlock()
}

func (d *Daemon) logRuntimeError(key string, err error) {
	now := time.Now()
	message := err.Error()
	d.errorMu.Lock()
	previous, found := d.observeErrors[key]
	if found && previous.message == message && now.Sub(previous.at) < time.Minute {
		d.errorMu.Unlock()
		return
	}
	d.observeErrors[key] = observedError{message: message, at: now}
	d.errorMu.Unlock()
	d.log.Printf("%s: %s", key, err)
}

func (d *Daemon) Run(ctx context.Context) error {
	defer d.sources.shutdown()
	d.log.Printf("started; %d surfaces", len(d.Surfaces))
	if recovered, err := d.Registry.RecoverInflight(time.Now().Add(-time.Minute)); err != nil {
		d.log.Printf("warn: recover inflight queue: %s", err)
	} else if recovered > 0 {
		d.log.Printf("dead-lettered %d message(s) with an uncertain delivery outcome", recovered)
	}
	d.startFederation(ctx)
	defer d.stopFederation()
	catalogDone := make(chan struct{})
	go func() {
		defer close(catalogDone)
		d.runCatalog(ctx)
	}()
	hostDone := make(chan struct{})
	go func() {
		defer close(hostDone)
		d.superviseClaudeHost(ctx)
	}()
	go d.sweepUploads(ctx)
	go d.runLimitResumes(ctx)
	uiDone := make(chan struct{})
	go func() {
		defer close(uiDone)
		d.watchUI(ctx)
	}()
	observerTicker := time.NewTicker(d.pollInterval)
	defer observerTicker.Stop()
	d.scanAndRelay(ctx)
	for {
		select {
		case <-ctx.Done():
			d.background.stop()
			<-catalogDone
			<-hostDone
			<-uiDone
			d.log.Printf("stopping")
			return nil
		case <-observerTicker.C:
			d.scanAndRelay(ctx)
		case <-d.observerNudge:
			d.scanAndRelay(ctx)
		}
	}
}

// runCatalog owns catalog discovery and the status pass. Each runs on its
// own goroutine, apart from relay observation, so neither a slow relay scan
// nor a slow discovery pass delays a status change.
func (d *Daemon) runCatalog(ctx context.Context) {
	statusDone := make(chan struct{})
	go func() {
		defer close(statusDone)
		d.runCatalogStatus(ctx)
	}()
	defer func() { <-statusDone }()
	defer d.catalogIdentities.wait()
	wakes := newCatalogWakes(d.Surfaces, filewake.FollowDir)
	defer wakes.close()
	discoveryTicker := time.NewTicker(catalogDiscoveryInterval)
	defer discoveryTicker.Stop()
	wakes.sync()
	d.discoverCatalog(ctx)
	wakes.sync()
	for {
		select {
		case <-ctx.Done():
			return
		case <-discoveryTicker.C:
			d.discoverCatalog(ctx)
			wakes.sync()
		case <-d.catalogNudge:
			d.discoverCatalog(ctx)
			wakes.sync()
		case <-wakes.signal:
			d.discoverCatalogOf(ctx, wakes.take())
		}
	}
}

func (d *Daemon) runCatalogStatus(ctx context.Context) {
	statusTicker := time.NewTicker(catalogStatusInterval)
	defer statusTicker.Stop()
	problemTicker := time.NewTicker(deliveryProblemInterval)
	defer problemTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-statusTicker.C:
			d.refreshCatalogStatus(ctx)
		case <-problemTicker.C:
			d.settleDeliveryProblems()
		}
	}
}

// settleDeliveryProblems dismisses the delivery failures that no longer need
// anyone and tells live clients.
func (d *Daemon) settleDeliveryProblems() {
	settled, err := d.Registry.SettleDeliveryProblems()
	if err != nil {
		d.log.Printf("settle delivery problems: %s", err)
	}
	if settled == 0 {
		return
	}
	if err := d.catalog.flushCommitted(); err != nil {
		d.log.Printf("publish settled delivery problems: %s", err)
	}
}

func (d *Daemon) RunWithSignal() error {
	lock, err := acquireDaemonLock()
	if err != nil {
		return err
	}
	defer releaseDaemonLock(lock)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	d.log.Printf("daemon pid %d", os.Getpid())
	if err := os.WriteFile(PidFilePath(), []byte(fmt.Sprintf("%d", os.Getpid())), 0600); err != nil {
		return fmt.Errorf("write pidfile: %w", err)
	}
	defer func() {
		if removeErr := removePIDFileIfOwned(os.Getpid()); removeErr != nil {
			d.log.Printf("warn: remove pidfile: %s", removeErr)
		}
	}()
	if stopDebug, err := d.startDebugSocket(); err != nil {
		d.log.Printf("debug socket unavailable: %s", err)
	} else {
		defer stopDebug()
	}
	stopPeers, err := d.startClaudePeers(ctx)
	if err != nil {
		d.log.Printf("Claude peer registration unavailable: %s", err)
	} else {
		defer stopPeers()
	}
	return d.Serve(ctx)
}

// Serve runs the daemon with its dashboard until ctx ends, then shuts the
// dashboard down.
func (d *Daemon) Serve(ctx context.Context) error {
	dashboard, err := d.startDashboard()
	if err != nil {
		return err
	}
	if dashboard != nil {
		defer func() {
			if shutdownErr := dashboard.shutdown(); shutdownErr != nil {
				d.log.Printf("dashboard shutdown: %s", shutdownErr)
			}
		}()
	}
	return d.Run(ctx)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func PidFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "daemon.pid")
}

func LogFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "daemon.log")
}

func IsRunning() (int, bool) {
	data, err := os.ReadFile(PidFilePath())
	if err != nil {
		return 0, false
	}
	var pid int
	fmt.Sscanf(string(data), "%d", &pid)
	if pid <= 0 {
		return 0, false
	}
	if err := exec.Command("kill", "-0", fmt.Sprintf("%d", pid)).Run(); err != nil || !processIsDaemon(pid) {
		return 0, false
	}
	return pid, true
}

func Stop() error {
	pid, ok := IsRunning()
	if !ok {
		return fmt.Errorf("daemon not running")
	}
	if err := exec.Command("kill", "-TERM", fmt.Sprintf("%d", pid)).Run(); err != nil {
		return err
	}
	if !waitForStopped(pid, 5*time.Second) {
		return fmt.Errorf("daemon pid %d did not stop within 5s", pid)
	}
	return nil
}
