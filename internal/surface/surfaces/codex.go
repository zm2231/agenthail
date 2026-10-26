package surfaces

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

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
	usageMu      sync.Mutex
	usageReads   map[string]*codexUsageRead
	planMu       sync.Mutex
	planState    map[string]*codexPlanState
	backgroundMu    sync.Mutex
	backgroundState map[string]*codexBackgroundState
	loadedThreads   map[string]map[string]bool
}

// codexDesktopRunsHere is whether Codex Desktop can run on this machine;
// Agenthail drives only its macOS app. Elsewhere nothing probes the default
// debugging port unless one is configured, since another program may own it.
var codexDesktopRunsHere = runtime.GOOS == "darwin"

func NewCodex(remoteURL string) *Codex {
	managed := remoteURL == "" || !strings.Contains(remoteURL, "://")
	if remoteURL == "" {
		if !codexDesktopRunsHere {
			return &Codex{managed: managed}
		}
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

func (c *Codex) Health(ctx context.Context) error {
	if err := c.Ready(ctx); err != nil {
		return fmt.Errorf("Codex session discovery is unavailable: %w", err)
	}
	return nil
}

func (c *Codex) Reply(ctx context.Context, sess *surface.Session, limit int) (*surface.ReplyResult, error) {
	return readSessionReply(ctx, c, sess, limit)
}

func (c *Codex) Tail(ctx context.Context, sess *surface.Session, n int) ([]surface.Exchange, error) {
	return surface.Tail(ctx, c, sess, n)
}
