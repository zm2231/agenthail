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
		return journalTurnOutcomes(func(before uint64) (registry.SessionJournalPage, error) {
			return b.d.Registry.ReadSessionJournalPage(sessionID, before, 200)
		}, turnIDs)
	})
}

func journalTurnOutcomes(readPage func(before uint64) (registry.SessionJournalPage, error), requested []string) (map[string]voice.TurnOutcome, error) {
	read := turnReads{turns: map[string]voice.TurnOutcome{}, took: map[string]string{}, wanted: map[string]bool{}}
	for _, id := range requested {
		read.wanted[id] = true
	}
	var before uint64
	for rows := 0; rows < voiceOutcomeRows && !read.answered(requested); {
		page, err := readPage(before)
		var gap *registry.SessionJournalHistoryGapError
		if before != 0 && errors.As(err, &gap) {
			break
		}
		if err != nil {
			return nil, err
		}
		for i := len(page.Entries) - 1; i >= 0; i-- {
			if event, err := sessionstream.DecodePayload(page.Entries[i].Seq, page.Entries[i].Payload); err == nil {
				read.add(event)
			}
		}
		rows += len(page.Entries)
		if page.NextBefore == 0 || len(page.Entries) == 0 {
			break
		}
		before = page.NextBefore
	}
	outcomes := make(map[string]voice.TurnOutcome, len(requested))
	for _, id := range requested {
		outcomes[id] = read.outcome(id)
	}
	return outcomes, nil
}

type turnReads struct {
	turns  map[string]voice.TurnOutcome
	took   map[string]string
	wanted map[string]bool
}

func (r turnReads) add(event sessionstream.Event) {
	if event.TurnID == "" {
		return
	}
	r.turns[event.TurnID] = withEvent(r.turns[event.TurnID], event)
	if _, seen := r.took[event.MessageID]; r.wanted[event.MessageID] && event.MessageID != event.TurnID && !seen {
		r.took[event.MessageID] = event.TurnID
	}
}

func (r turnReads) outcome(id string) voice.TurnOutcome {
	turn, ok := r.took[id]
	if !ok {
		return r.turns[id]
	}
	outcome := r.turns[turn]
	outcome.TurnID = turn
	return outcome
}

func (r turnReads) answered(requested []string) bool {
	for _, id := range requested {
		if outcome := r.outcome(id); !outcome.Done || outcome.Reply == "" {
			return false
		}
	}
	return true
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
