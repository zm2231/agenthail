package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/apitime"
	"github.com/zm2231/agenthail/internal/ledger"
)

type mcpLedgerInput struct {
	View    string `json:"view,omitempty" jsonschema:"board (capture, sync, counts, goals and the newest provisional words; the default), goals, asks, decisions, questions, work, ready (the next work, ranked), backlog (parked work, ready first), triage (unowned work idle 14 days), item (one work item or ask with its context, by id), or words (one captured message with its context, by utterance id)"`
	ID      string `json:"id,omitempty" jsonschema:"the work item, work action or ask id for item; the utterance id for words"`
	Context int    `json:"context,omitempty" jsonschema:"words: also this many turns before and after it, 0 to 5, read on the capturing host"`
	Limit   int    `json:"limit,omitempty" jsonschema:"entries per page, 1 to 100 (ready: 1 to 50); default 25"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"nextCursor from a previous call"`
}

func (d *Daemon) mcpLedgerRead(_ context.Context, caller *mcpCaller, input mcpLedgerInput) (any, error) {
	if !caller.has("ledger.read") {
		return nil, refuse("capability_missing", "Reading the ledger needs ledger.read.")
	}
	key, err := LedgerKey(d.Registry, caller.project)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, refuse("invalid", "Your session is not in a project with a ledger; the person adds one with agenthail project add.")
	}
	if input.View == "words" {
		return d.mcpLedgerWords(key, input)
	}
	result, err := d.ledgerRead(key, ledger.ReadRequest{View: input.View, ID: input.ID, Limit: input.Limit, Cursor: input.Cursor, Budget: mcpPageBudget})
	var refusal *ledger.Refusal
	var missing *ledger.NotFound
	switch {
	case errors.As(err, &refusal):
		return nil, refuse("invalid", refusal.Reason+".")
	case errors.As(err, &missing):
		return nil, refuse("not_found", missing.Error()+".")
	}
	return result, err
}

const (
	mcpWordsAround    = 5
	mcpWordsTextBytes = 2000
	mcpWordsQuotes    = 8
	mcpWordsIDBytes   = 256
)

func (d *Daemon) mcpLedgerWords(key string, input mcpLedgerInput) (any, error) {
	if input.Context < 0 || input.Context > mcpWordsAround {
		return nil, refuse("invalid", fmt.Sprintf("context takes 0 to %d turns here; agenthail ledger words reads up to %d.", mcpWordsAround, WordsMaxAround))
	}
	words, err := ReadWords(d.Registry, d.ledgerStore(), key, input.ID, input.Context)
	var elsewhere WordsElsewhere
	var missing WordsNotFound
	switch {
	case errors.As(err, &elsewhere):
		return nil, refuse("unavailable", err.Error()+".")
	case errors.As(err, &missing):
		return nil, refuse("not_found", err.Error()+".")
	case err != nil:
		return nil, err
	}
	return fitWords(words)
}

func fitWords(words Words) (Words, error) {
	for limit := mcpWordsTextBytes; limit > 0; limit /= 2 {
		fitted := clipWords(words, limit, limit*2/5)
		encoded, err := apitime.Marshal(fitted)
		if err != nil {
			return Words{}, err
		}
		if len(encoded) <= mcpOutputLimit {
			return fitted, nil
		}
	}
	return Words{}, refuse("invalid", fmt.Sprintf("These words do not fit in %d bytes; agenthail ledger words shows them in full.", mcpOutputLimit))
}

func clipWords(words Words, text, part int) Words {
	id := func(s string) string { return clipHead(s, min(mcpWordsIDBytes, part)) }
	words.ID, words.TargetID, words.At, words.Host = id(words.ID), id(words.TargetID), id(words.At), id(words.Host)
	words.Text, words.Typed = clipHead(words.Text, text), clipHead(words.Typed, text)
	if len(words.Quoted) > mcpWordsQuotes {
		words.MoreQuoted = len(words.Quoted) - mcpWordsQuotes
	}
	quoted := make([]string, 0, min(len(words.Quoted), mcpWordsQuotes))
	for _, q := range words.Quoted[:min(len(words.Quoted), mcpWordsQuotes)] {
		quoted = append(quoted, clipHead(q, part))
	}
	words.Quoted = quoted
	if words.Context = ledger.ClipReply(words.Context); words.Context != nil {
		c := words.Context
		c.Quotes = nil
		c.UtteranceID, c.Status, c.JoinedBy, c.EntryItemID = id(c.UtteranceID), id(c.Status), id(c.JoinedBy), id(c.EntryItemID)
		if r := c.Reply; r != nil {
			r.Text = clipTail(r.Text, part)
			r.ItemID, r.TS, r.SHA256 = id(r.ItemID), id(r.TS), id(r.SHA256)
		}
	}
	words.Turns = slices.Clone(words.Turns)
	for i := range words.Turns {
		turn := &words.Turns[i]
		turn.ItemID, turn.Role, turn.TS = id(turn.ItemID), id(turn.Role), id(turn.TS)
		if clipped := clipHead(words.Turns[i].Text, part); clipped != words.Turns[i].Text {
			words.Turns[i].Text, words.Turns[i].Truncated = clipped, true
		}
	}
	return words
}

func clipHead(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit] + "…"
}

func clipTail(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return "…" + text[start:]
}
