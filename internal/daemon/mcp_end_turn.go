package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
)

type mcpEndTurnInput struct {
	Headline  string   `json:"headline" jsonschema:"One line for the person, at most 80 characters; a longer one is cut."`
	Next      []string `json:"next,omitempty" jsonschema:"Up to 3 follow-ups."`
	WaitingOn string   `json:"waitingOn,omitempty" jsonschema:"Only if the work stops until the person acts: what they must do, in 120 characters. For a choice, use agenthail_ask."`
	Item      string   `json:"item,omitempty" jsonschema:"The item it holds up; default your one in progress."`
}

const waitingOnRunes = 120

func (d *Daemon) mcpEndTurn(ctx context.Context, caller *mcpCaller, input mcpEndTurnInput) (any, error) {
	headline, next, refusal := endTurnLines(input)
	if refusal != nil {
		return nil, refusal
	}
	session := caller.session
	end := registry.TurnEnd{SessionID: session.ID, Headline: headline, Next: next, Source: registry.TurnSourceTool, WaitingOn: oneLine(input.WaitingOn)}
	item := strings.TrimSpace(input.Item)
	note := ""
	switch {
	case utf8.RuneCountInString(end.WaitingOn) > waitingOnRunes:
		return nil, refuse("invalid", fmt.Sprintf("waitingOn is at most %d characters: say in one line what the person has to do.", waitingOnRunes))
	case end.WaitingOn == "" && item != "":
		return nil, refuse("invalid", "item names the work a wait holds up; give waitingOn with it, or leave both out.")
	case end.WaitingOn != "":
		work, why, refusal := d.linkedWork(session, item)
		if refusal != nil {
			return nil, refusal
		}
		end.WorkItem, end.LedgerKey, note = work.item.ID, work.key, why
	}
	runtime, _, err := d.Registry.RuntimeState(session.ID)
	if err != nil {
		return nil, err
	}
	if _, err := d.Registry.EndTurn(end, runtime.ActiveTurnID, time.Now()); err != nil {
		return nil, err
	}
	d.requestCatalogDiscovery()
	d.publishEvent("state.changed", session.ID, map[string]string{"source": "end-turn"})
	then := endTurnThen
	if feedback := headlineFeedback(headline); feedback != "" {
		then = feedback
	}
	reply := map[string]any{"ok": true, "then": then}
	if note != "" {
		reply["note"] = note
	}
	return reply, nil
}

const endTurnThen = "Turn recorded. Now write your reply to the person as your final message, and call no more tools."

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
