package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) createNotionThread(ctx context.Context, message, alias, model string) (*delivery.Receipt, error) {
	adapter := d.surfaceForKind(surface.KindNotion)
	if adapter == nil {
		return nil, fmt.Errorf("Notion is not configured")
	}
	message = strings.TrimSpace(message)
	alias = strings.TrimPrefix(strings.TrimSpace(alias), "@")
	if message == "" {
		return nil, fmt.Errorf("first message is required")
	}
	session := &surface.Session{ID: "new", Surface: surface.KindNotion, Name: alias, Status: surface.StatusIdle}
	var result *surface.SendResult
	var err error
	if model = strings.TrimSpace(model); model != "" {
		sender, ok := adapter.(surface.OptionSender)
		if !ok {
			return nil, fmt.Errorf("Notion does not support model selection")
		}
		result, err = sender.SendWithOptions(ctx, session, message, surface.SendOptions{Model: model})
	} else {
		result, err = adapter.Send(ctx, session, message)
	}
	if err != nil {
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", Message: message, Error: err.Error()})
		return nil, err
	}
	if result == nil || !result.Accepted || result.UUID == "" {
		return nil, fmt.Errorf("Notion created a thread without returning its id")
	}
	session.ID = result.UUID
	session.LastActive = time.Now()
	if err := d.Registry.RegisterSession(*session); err != nil {
		return nil, fmt.Errorf("Notion thread %s was created but could not be registered: %w", session.ID, err)
	}
	if alias != "" {
		if err := d.Registry.SetAlias(alias, session.ID); err != nil {
			return nil, fmt.Errorf("Notion thread %s was created but alias %q could not be registered: %w", session.ID, alias, err)
		}
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "sent", SessionID: session.ID, Message: message, Result: result.UUID})
	return &delivery.Receipt{Disposition: delivery.DispositionAccepted, SessionID: session.ID, TurnID: result.UUID}, nil
}
