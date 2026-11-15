package daemon

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
)

type mcpEndTurnInput struct {
	Headline string   `json:"headline" jsonschema:"One line for the person; a long one is cut where it is shown."`
	Next     []string `json:"next,omitempty" jsonschema:"Up to 3 follow-ups."`
}

func (d *Daemon) mcpEndTurn(ctx context.Context, caller *mcpCaller, input mcpEndTurnInput) (any, error) {
	headline, next, refusal := endTurnLines(input)
	if refusal != nil {
		return nil, refusal
	}
	session := caller.session
	runtime, _, err := d.Registry.RuntimeState(session.ID)
	if err != nil {
		return nil, err
	}
	end := registry.TurnEnd{SessionID: session.ID, Headline: headline, Next: next, Source: registry.TurnSourceTool}
	if _, err := d.Registry.EndTurn(end, runtime.ActiveTurnID, time.Now()); err != nil {
		return nil, err
	}
	d.requestCatalogDiscovery()
	d.publishEvent("state.changed", session.ID, map[string]string{"source": "end-turn"})
	result := map[string]any{"ok": true}
	if line := d.backlogReadyLine(ctx, caller, time.Now()); line != "" {
		result["backlog"] = line
	}
	return result, nil
}

const endTurnStoredRunes = 1000

func endTurnLines(input mcpEndTurnInput) (string, []string, *mcpRefusal) {
	headline := clipRunes(oneLine(input.Headline), endTurnStoredRunes)
	if headline == "" {
		return "", nil, refuse("invalid", "headline is required.")
	}
	var next []string
	for _, item := range input.Next {
		if item = clipRunes(oneLine(item), endTurnStoredRunes); item != "" && len(next) < 3 {
			next = append(next, item)
		}
	}
	return headline, next, nil
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

func clipRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}
