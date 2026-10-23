package daemon

import (
	"context"
	"errors"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
)

const voiceOutcomeRows = 1000

func (b voiceBriefs) TurnOutcomes(ctx context.Context, session *surface.Session, turnIDs []string) (map[string]voice.TurnOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, voiceBriefTimeout)
	defer cancel()
	sessionID := session.ID
	return within(ctx, func() (map[string]voice.TurnOutcome, error) {
		return b.d.journalTurnOutcomes(sessionID, turnIDs)
	})
}

func (d *Daemon) journalTurnOutcomes(sessionID string, turnIDs []string) (map[string]voice.TurnOutcome, error) {
	outcomes := make(map[string]voice.TurnOutcome, len(turnIDs))
	open := map[string]bool{}
	for _, id := range turnIDs {
		outcomes[id], open[id] = voice.TurnOutcome{}, true
	}
	var before uint64
	for read := 0; read < voiceOutcomeRows && len(open) > 0; {
		page, err := d.Registry.ReadSessionJournalPage(sessionID, before, 200)
		var gap *registry.SessionJournalHistoryGapError
		if before != 0 && errors.As(err, &gap) {
			break
		}
		if err != nil {
			return nil, err
		}
		for i := len(page.Entries) - 1; i >= 0; i-- {
			event, err := sessionstream.DecodePayload(page.Entries[i].Seq, page.Entries[i].Payload)
			if err != nil || !open[event.TurnID] {
				continue
			}
			outcome := withEvent(outcomes[event.TurnID], event)
			outcomes[event.TurnID] = outcome
			if outcome.Done && outcome.Reply != "" {
				delete(open, event.TurnID)
			}
		}
		read += len(page.Entries)
		if page.NextBefore == 0 || len(page.Entries) == 0 {
			break
		}
		before = page.NextBefore
	}
	return outcomes, nil
}

// Events arrive newest first, so the first assistant text seen is the latest.
func withEvent(outcome voice.TurnOutcome, event sessionstream.Event) voice.TurnOutcome {
	outcome.Found = true
	switch {
	case event.Kind == "done":
		outcome.Done = true
		if event.Failed() {
			outcome.Failed = event.FailureReason()
		}
	case event.SpokenAssistantContent() && outcome.Reply == "":
		outcome.Reply = event.Body
	}
	return outcome
}
