package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
)

type mcpEndTurnInput struct {
	Headline string   `json:"headline" jsonschema:"One line, at most 80 characters."`
	Next     []string `json:"next,omitempty" jsonschema:"At most 3 follow-ups."`
}

func (d *Daemon) mcpEndTurn(_ context.Context, caller *mcpCaller, input mcpEndTurnInput) (any, error) {
	if refusal := validEndTurn(input); refusal != nil {
		return nil, refusal
	}
	session := caller.session
	runtime, _, err := d.Registry.RuntimeState(session.ID)
	if err != nil {
		return nil, err
	}
	end := registry.TurnEnd{SessionID: session.ID, Headline: strings.TrimSpace(input.Headline), Next: input.Next, Source: registry.TurnSourceTool}
	if _, err := d.Registry.EndTurn(end, runtime.ActiveTurnID, time.Now()); errors.Is(err, registry.ErrTurnAlreadyEnded) {
		return nil, refuse("already_ended", "You already ended this turn. Stop here; the next turn can end again.")
	} else if err != nil {
		return nil, err
	}
	d.requestCatalogDiscovery()
	d.publishEvent("state.changed", session.ID, map[string]string{"source": "end-turn"})
	result := map[string]any{"ok": true}
	if line := d.backlogReadyLine(caller, time.Now()); line != "" {
		result["backlog"] = line
	}
	return result, nil
}

func validEndTurn(input mcpEndTurnInput) *mcpRefusal {
	tooLong := func(field, value string, limit int) *mcpRefusal {
		if utf8.RuneCountInString(value) > limit {
			return refuse("invalid", fmt.Sprintf("%s is at most %d characters.", field, limit))
		}
		return nil
	}
	headline := strings.TrimSpace(input.Headline)
	if headline == "" || strings.ContainsAny(headline, "\r\n") {
		return refuse("invalid", "headline is one line and required.")
	}
	if refusal := tooLong("headline", headline, 80); refusal != nil {
		return refusal
	}
	if len(input.Next) > 3 {
		return refuse("invalid", "at most 3 next items.")
	}
	for _, next := range input.Next {
		if strings.TrimSpace(next) == "" {
			return refuse("invalid", "a next item is empty.")
		}
		if refusal := tooLong("a next item", next, 160); refusal != nil {
			return refusal
		}
	}
	return nil
}
