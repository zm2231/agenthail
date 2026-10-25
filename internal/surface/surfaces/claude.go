package surfaces

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

type Claude struct {
	signins      signin.Resolver
	home         string
	cookieBridge string
	contextMu    sync.Mutex
	contextState map[string]*claudeContextState
	observeMu    sync.Mutex
	observeState map[string]*claudeObservationState
	goalMu       sync.Mutex
	goals        map[string]*claudeGoalCursor
	tasksMu      sync.Mutex
	taskScans    map[string]*claudeTaskScan
	taskPrune    sync.Once
	modelsMu     sync.Mutex
	modelsCache  []surface.ModelOption
	modelsAt     time.Time
	modelsFlight *claudeModelsFlight
	subagents    *claudeSubagentObserver
	transcripts  *claudeTranscriptIndex
	host         *claudeHost
	prompts      *claudePrompts
	request      ClaudeRequest
	webAccounts  claudeWebAccounts
	localOnly    bool
}

type ClaudeRequest func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error)

func NewClaude(signins signin.Resolver, home string) *Claude {
	return NewClaudeWithRequest(signins, home, claudeSendRequest)
}

func NewClaudeWithRequest(signins signin.Resolver, home string, request ClaudeRequest) *Claude {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return &Claude{signins: signins, home: home, cookieBridge: configuredCookieBridge(), subagents: newClaudeSubagentObserver(), request: request, transcripts: newClaudeTranscriptIndex(), host: newClaudeHost(home), prompts: newClaudePrompts(), webAccounts: claudeWebAccounts{byProfile: map[string]claudeWebAccountEntry{}}}
}

func (c *Claude) Name() surface.SurfaceKind { return surface.KindClaude }

func (c *Claude) RuntimeKinds() []surface.RuntimeKind {
	return []surface.RuntimeKind{surface.RuntimeClaudeCLI, surface.RuntimeClaudeHost, surface.RuntimeClaudeBackground}
}

func (c *Claude) ObserveClaudeRuns(ctx context.Context) ([]surface.ClaudeRunObservation, error) {
	return ObserveClaudeRuns(ctx, c.home)
}

func (c *Claude) ObserveClaudeSubagents(ctx context.Context, session *surface.Session) ([]surface.ClaudeSubagentLink, error) {
	return c.subagents.observeSession(ctx, session)
}

func (c *Claude) ObserveAllClaudeSubagents(ctx context.Context) ([]surface.ClaudeSubagentLink, error) {
	return ObserveAllClaudeSubagents(ctx, c.home)
}

func (c *Claude) Capabilities() surface.Capabilities {
	return surface.Capabilities{
		Send: true, Stream: true, Reply: true, GoalSet: true, GoalStatus: true,
		Compact: true, Model: true, Interrupt: true, Steer: true,
		Attach: true, Commands: true,
		Lifecycle: surface.Lifecycle{Status: true, Logs: true, Stop: true, Resume: true, Adopt: true},
	}
}
