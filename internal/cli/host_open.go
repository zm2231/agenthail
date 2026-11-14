package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/targets"
)

// throughDaemon names a session of this host whose conversation only the
// daemon can open as a target the daemon acts on, as it does for a session on
// another host.
func (a *App) throughDaemon(sess *surface.Session) (*remoteTarget, error) {
	if !a.daemonIsRunning() {
		return nil, fmt.Errorf("%w; no daemon is running to open %s, so start it with 'agenthail daemon start' and try again", surface.ErrOpenIsTheDaemons, a.resolveDisplay(sess.ID))
	}
	host, err := a.registry().HostIdentity()
	if err != nil {
		return nil, fmt.Errorf("read host identity: %w", err)
	}
	alias, _ := a.registry().ReverseAlias(sess.ID)
	return &remoteTarget{ID: sess.ID, Surface: sess.Surface, Name: sess.Name, Alias: alias, Host: targets.Host{ID: host.ID, Name: host.Name}, Exact: true}, nil
}

func (a *App) sendThroughDaemon(ctx context.Context, sess *surface.Session, message string, options surface.SendOptions, scopeProjectID string, noQueue bool) (*delivery.Receipt, error) {
	target, err := a.throughDaemon(sess)
	if err != nil {
		return nil, err
	}
	payload, err := actionPayload(options.TurnOptions)
	if err != nil {
		return nil, err
	}
	payload["action"], payload["message"] = "send", message
	if options.SourceSessionID != "" {
		payload["sourceSessionId"] = options.SourceSessionID
	}
	if options.Model != "" {
		payload["model"] = options.Model
	}
	if scopeProjectID != "" {
		payload["scopeProjectId"] = scopeProjectID
	}
	if noQueue {
		payload["noQueue"] = true
	}
	result, err := a.remoteAction(ctx, target, "send to", payload)
	if err != nil {
		return nil, err
	}
	var receipt delivery.Receipt
	if err := json.Unmarshal(result, &receipt); err != nil {
		return nil, err
	}
	if fresh, err := a.registry().Session(sess.ID); err == nil {
		*sess = *fresh
	}
	return &receipt, nil
}

func (a *App) adoptThroughDaemon(ctx context.Context, sess *surface.Session, request surface.AdoptRequest) (*surface.AdoptResult, error) {
	target, err := a.throughDaemon(sess)
	if err != nil {
		return nil, err
	}
	data, err := a.remoteAction(ctx, target, "open", map[string]any{"action": "session-adopt", "stopTerminal": request.StopTerminal, "waitMs": request.Wait.Milliseconds()})
	if err != nil {
		return nil, err
	}
	var result surface.AdoptResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
