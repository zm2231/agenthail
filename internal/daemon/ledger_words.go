package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	WordsMaxAround  = 20
	wordsAnchorID   = "entry"
	wordsAnchorTime = "time"
)

type Words struct {
	ID         string                 `json:"id"`
	TargetID   string                 `json:"targetId"`
	At         string                 `json:"ts"`
	Host       string                 `json:"host"`
	Text       string                 `json:"text"`
	Typed      string                 `json:"typed"`
	Quoted     []string               `json:"quoted,omitempty"`
	MoreQuoted int                    `json:"moreQuoted,omitempty"`
	Context    *ledger.ContextPayload `json:"context,omitempty"`
	Anchor     string                 `json:"anchor,omitempty"`
	Turns      []WordsTurn            `json:"turns,omitempty"`
}

type WordsTurn struct {
	ItemID    string `json:"itemId"`
	Role      string `json:"role"`
	TS        string `json:"ts"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
	Message   bool   `json:"message,omitempty"`
}

type WordsNotFound struct{ Key, Ref string }

func (e WordsNotFound) Error() string {
	return fmt.Sprintf("%s has no captured words %s", e.Key, e.Ref)
}

type WordsElsewhere struct{ Host string }

func (e WordsElsewhere) Error() string {
	return fmt.Sprintf("these words were captured on %s, which holds the session's journal; read the turns around them there", e.Host)
}

// Only the capturing host holds the session journal the turns come from; elsewhere they are refused.
func ReadWords(r *registry.Registry, store *ledger.Store, key, ref string, around int) (Words, error) {
	if around < 0 || around > WordsMaxAround {
		return Words{}, fmt.Errorf("--context takes 0 to %d turns", WordsMaxAround)
	}
	captured, ok, err := store.CapturedWords(key, ref)
	if err != nil {
		return Words{}, err
	}
	if !ok {
		return Words{}, WordsNotFound{Key: key, Ref: ref}
	}
	self, err := r.HostIdentity()
	if err != nil {
		return Words{}, err
	}
	if captured.HostID == "" {
		// Events written before origin stamping carry no host; only this host could have written them.
		captured.HostID = self.ID
	}
	words := Words{ID: captured.ID, TargetID: captured.TargetID, At: captured.At, Host: hostName(r, self, captured.HostID), Text: captured.Text, Context: captured.Context}
	var quotes []ledger.Quote
	if captured.Context != nil {
		quotes = captured.Context.Quotes
	}
	words.Typed, words.Quoted = ledger.Typed(captured.Text, quotes), ledger.QuotedText(captured.Text, quotes)
	if around == 0 {
		return words, nil
	}
	if captured.HostID != self.ID {
		return words, WordsElsewhere{Host: words.Host}
	}
	turns, err := journalTurns(r, captured.TargetID)
	if err != nil {
		return words, err
	}
	words.Anchor, words.Turns = turnsAround(turns, captured, around)
	return words, nil
}

func hostName(r *registry.Registry, self registry.HostIdentity, id string) string {
	if id == self.ID {
		return self.Name
	}
	if host, err := r.Host(id); err == nil && host.Name != "" {
		return host.Name
	}
	return id
}

func journalTurns(r *registry.Registry, sessionID string) ([]WordsTurn, error) {
	var turns []WordsTurn
	var cursor uint64
	for rows := 0; rows < sessionJournalRetentionCount; {
		page, err := r.ReadSessionJournalPage(sessionID, cursor, contextPage)
		var gap *registry.SessionJournalHistoryGapError
		if cursor != 0 && errors.As(err, &gap) {
			break
		}
		if err != nil {
			return nil, err
		}
		for i := len(page.Entries) - 1; i >= 0; i-- {
			var p sessionJournalPayload
			if json.Unmarshal(page.Entries[i].Payload, &p) != nil || p.Body == "" || (p.Role != "user" && p.Role != "assistant") || (p.Kind != "message" && p.Kind != "text") {
				continue
			}
			turns = append(turns, WordsTurn{ItemID: p.ItemID, Role: p.Role, TS: p.TS, Text: p.Body, Truncated: p.Truncated})
		}
		rows += len(page.Entries)
		if page.NextBefore == 0 || len(page.Entries) == 0 {
			break
		}
		cursor = page.NextBefore
	}
	slices.Reverse(turns)
	return turns, nil
}

func turnsAround(turns []WordsTurn, captured ledger.CapturedWords, around int) (string, []WordsTurn) {
	if c := captured.Context; c != nil && c.EntryItemID != "" {
		for i, turn := range turns {
			if turn.ItemID == c.EntryItemID {
				out := slices.Clone(turns[max(0, i-around):min(len(turns), i+around+1)])
				out[min(i, around)].Message = true
				return wordsAnchorID, out
			}
		}
	}
	at, err := time.Parse(time.RFC3339Nano, captured.At)
	if err != nil {
		return wordsAnchorTime, nil
	}
	split := len(turns)
	for i, turn := range turns {
		if ts, err := time.Parse(time.RFC3339Nano, turn.TS); err == nil && !ts.Before(at) {
			split = i
			break
		}
	}
	return wordsAnchorTime, slices.Clone(turns[max(0, split-around):min(len(turns), split+around)])
}
