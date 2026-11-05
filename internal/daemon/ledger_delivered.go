package daemon

import (
	"strings"
	"sync"
	"time"
)

// A delivery waits for its prompt hook as long as a queued message can wait
// for its turn; once claimed, it waits only for the other copies of the hook,
// which report the same prompt within moments.
const (
	deliveredTextWindow = 2 * time.Minute
	deliveredRetention  = 24 * time.Hour
	deliveredPerSession = 64
)

// A conversation's first turn is sent before the session is known.
const startingSession = ""

// Held until the session's prompt hook sees them, so both captures name one
// utterance. A claimed delivery stays bound to the prompt that claimed it,
// since every copy of the hook reports that prompt.
type deliveredWords struct {
	mu        sync.Mutex
	bySession map[string][]deliveredEntry
}

// turn is the prompt Claude receives; words are the person's part of it.
type deliveredEntry struct {
	uuid     string
	turn     string
	words    string
	at       time.Time
	promptID string
	claimed  time.Time
}

func (w *deliveredWords) add(sessionID, uuid, text string, at time.Time) {
	w.hold(sessionID, deliveredEntry{uuid: uuid, turn: strings.TrimSpace(text), words: text, at: at})
}

func (w *deliveredWords) addStart(uuid, turn, words string, at time.Time) {
	w.hold(startingSession, deliveredEntry{uuid: uuid, turn: strings.TrimSpace(turn), words: words, at: at})
}

func (w *deliveredWords) hold(sessionID string, entry deliveredEntry) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.bySession == nil {
		w.bySession = map[string][]deliveredEntry{}
	}
	w.prune(entry.at)
	entries := append(w.bySession[sessionID], entry)
	if len(entries) > deliveredPerSession {
		entries = entries[len(entries)-deliveredPerSession:]
	}
	w.bySession[sessionID] = entries
}

func (w *deliveredWords) claim(sessionID, promptID, uuid, text string, now time.Time) (string, string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prune(now)
	text = strings.TrimSpace(text)
	for _, bucket := range []string{sessionID, startingSession} {
		if entry, ok := claimEntry(w.bySession[bucket], promptID, uuid, text, now); ok {
			return entry.uuid, entry.words, true
		}
	}
	return "", "", false
}

func claimEntry(entries []deliveredEntry, promptID, uuid, text string, now time.Time) (deliveredEntry, bool) {
	found := -1
	for i, entry := range entries {
		if entry.promptID == promptID || entry.promptID == "" && uuid != "" && entry.uuid == uuid {
			found = i
			break
		}
	}
	if found < 0 {
		for i, entry := range entries {
			if entry.promptID == "" && entry.turn == text && now.Sub(entry.at) <= deliveredTextWindow {
				found = i
				break
			}
		}
	}
	if found < 0 {
		return deliveredEntry{}, false
	}
	if entries[found].promptID == "" {
		entries[found].promptID, entries[found].claimed = promptID, now
	}
	return entries[found], true
}

func (w *deliveredWords) prune(now time.Time) {
	for sessionID, entries := range w.bySession {
		kept := entries[:0]
		for _, entry := range entries {
			if entry.promptID == "" && now.Sub(entry.at) <= deliveredRetention || entry.promptID != "" && now.Sub(entry.claimed) <= deliveredTextWindow {
				kept = append(kept, entry)
			}
		}
		if len(kept) == 0 {
			delete(w.bySession, sessionID)
		} else {
			w.bySession[sessionID] = kept
		}
	}
}
