package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	endTurnNeedsYou     = "you"
	endTurnNeedsNothing = "nothing"
)

type mcpEndTurnInput struct {
	Headline string             `json:"headline" jsonschema:"One line, at most 80 characters."`
	Needs    string             `json:"needs" jsonschema:"you or nothing."`
	Asks     []registry.TurnAsk `json:"asks,omitempty" jsonschema:"With needs you, 1 to 4. Limits: question 160 characters, 1 to 6 options, label 40, does 90; suggested is an option index; at most 3 links."`
	Next     []string           `json:"next,omitempty" jsonschema:"With needs nothing, at most 3 follow-ups."`
}

func (d *Daemon) mcpEndTurn(_ context.Context, caller *mcpCaller, input mcpEndTurnInput) (any, error) {
	if refusal := validEndTurn(input); refusal != nil {
		return nil, refusal
	}
	session := caller.session
	end := registry.TurnEnd{SessionID: session.ID, Needs: input.Needs, Headline: strings.TrimSpace(input.Headline), Asks: input.Asks, Next: input.Next, Source: "tool"}
	hearer, routed := noticeRecipient{}, false
	if end.Needs == endTurnNeedsYou {
		hearer, routed = d.endTurnHearer(session)
	}
	if routed {
		end.RoutedTo = hearer.sessionID
	}
	runtime, _, err := d.Registry.RuntimeState(session.ID)
	if err != nil {
		return nil, err
	}
	stored, err := d.Registry.EndTurn(end, runtime.ActiveTurnID, time.Now())
	if errors.Is(err, registry.ErrTurnAlreadyEnded) {
		return nil, refuse("already_ended", "You already ended this turn. Stop here; the next turn can end again.")
	}
	if err != nil {
		return nil, err
	}
	if routed {
		if _, err := d.sendNotice(hearer, endTurnNotice(d.resolveDisplay(session.ID), d.answerTarget(session.ID), stored), endTurnNoticeKey(stored)); err != nil {
			d.log.Printf("tell %s that %s needs it: %s", hearer.role, d.resolveDisplay(session.ID), err)
			routed = false
			if err := d.Registry.RouteTurnEnd(session.ID, stored.StartedAt, ""); err != nil {
				return nil, err
			}
		}
	}
	d.requestCatalogDiscovery()
	d.publishEvent("state.changed", session.ID, map[string]string{"source": "end-turn"})
	result := map[string]any{"ok": true, "needs": stored.Needs}
	if routed {
		result["askedSession"] = hearer.sessionID
	}
	return result, nil
}

// Only a session an agent started reports to one; the person hears the rest.
func (d *Daemon) endTurnHearer(session surface.Session) (noticeRecipient, bool) {
	origin, found, err := d.Registry.SessionOrigin(session.ID)
	if err != nil || !found || origin.Via != registry.OriginAgent {
		return noticeRecipient{}, false
	}
	return d.noticeRecipient(session)
}

func endTurnNoticeKey(end registry.TurnEnd) string {
	return fmt.Sprintf("notice-end-turn-%s-%d", end.SessionID, end.StartedAt.UnixMilli())
}

func endTurnNotice(name, target string, end registry.TurnEnd) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[agenthail] %s needs you: %s", name, end.Headline)
	for i, ask := range end.Asks {
		labels := make([]string, len(ask.Options))
		for j, option := range ask.Options {
			labels[j] = option.Label
			if option.Does != "" {
				labels[j] += " (" + option.Does + ")"
			}
			if ask.Suggested != nil && *ask.Suggested == j {
				labels[j] += " [suggested]"
			}
		}
		fmt.Fprintf(&b, "\n%d. %s %s", i+1, ask.Question, strings.Join(labels, " | "))
	}
	fmt.Fprintf(&b, "\nAnswer it: agenthail send %s \"<answer>\"", target)
	return b.String()
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
	switch input.Needs {
	case endTurnNeedsYou:
		if len(input.Asks) == 0 {
			return refuse("invalid", "needs you takes at least one ask: what must the person decide?")
		}
		if len(input.Next) > 0 {
			return refuse("invalid", "next goes with needs nothing; put choices for the person in asks.")
		}
	case endTurnNeedsNothing:
		if len(input.Asks) > 0 {
			return refuse("invalid", "asks go with needs you; with needs nothing, drop them or use next.")
		}
	default:
		return refuse("invalid", "needs is you or nothing.")
	}
	if len(input.Asks) > 4 {
		return refuse("invalid", "at most 4 asks.")
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
	for i, ask := range input.Asks {
		which := fmt.Sprintf("ask %d", i+1)
		if strings.TrimSpace(ask.Question) == "" {
			return refuse("invalid", which+" has no question.")
		}
		if refusal := tooLong(which+" question", ask.Question, 160); refusal != nil {
			return refusal
		}
		if len(ask.Options) == 0 || len(ask.Options) > 6 {
			return refuse("invalid", which+" takes 1 to 6 options.")
		}
		for _, option := range ask.Options {
			if strings.TrimSpace(option.Label) == "" {
				return refuse("invalid", which+" has an option with no label.")
			}
			if refusal := tooLong(which+" option label", option.Label, 40); refusal != nil {
				return refusal
			}
			if refusal := tooLong(which+" option does", option.Does, 90); refusal != nil {
				return refusal
			}
		}
		if ask.Suggested != nil && (*ask.Suggested < 0 || *ask.Suggested >= len(ask.Options)) {
			return refuse("invalid", fmt.Sprintf("%s suggested is an option index from 0 to %d.", which, len(ask.Options)-1))
		}
		if len(ask.Links) > 3 {
			return refuse("invalid", which+" takes at most 3 links.")
		}
		for _, link := range ask.Links {
			parsed, err := url.Parse(link.URL)
			if strings.TrimSpace(link.Title) == "" || err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				return refuse("invalid", which+" has a link without a title or an http(s) url.")
			}
		}
	}
	return nil
}
