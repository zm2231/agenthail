package codexsurface

import (
	"context"
	"sync"

	"github.com/zm2231/agenthail/internal/surface"
)

// TurnContext is what Agenthail tells a thread with a turn it starts, and
// delivered, called once Codex has taken the turn. Empty text adds nothing.
type TurnContext func(ctx context.Context, session surface.Session) (text string, delivered func())

const agenthailContextKey = "agenthail"

type turnContextAttachment struct {
	mu      sync.Mutex
	context TurnContext
}

func (a *turnContextAttachment) SetTurnContext(context TurnContext) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.context = context
}

// withTurnContext adds the context as a developer message Codex records in
// the turn. Codex takes additionalContext only from a connection that
// negotiated experimentalApi, and refuses the whole turn otherwise.
func (a *turnContextAttachment) withTurnContext(ctx context.Context, client codexClient, session surface.Session, params map[string]any) func() {
	a.mu.Lock()
	turnContext := a.context
	a.mu.Unlock()
	if _, experimental := client.(*managedCodexClient); !experimental || turnContext == nil {
		return func() {}
	}
	text, delivered := turnContext(ctx, session)
	if text == "" {
		return func() {}
	}
	params["additionalContext"] = map[string]any{agenthailContextKey: map[string]any{"value": text, "kind": "application"}}
	return delivered
}
