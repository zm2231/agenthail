package daemon

import (
	"strings"
	"sync"
	"time"
)

const (
	deliveredTextWindow = 2 * time.Minute
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
	entries := append(w.bySession[sessionID], entry)
	if len(entries) > deliveredPerSession {
		entries = entries[len(entries)-deliveredPerSession:]
	}
	w.bySession[sessionID] = entries
}

func (w *deliveredWords) claim(sessionID, promptID, uuid, text string, now time.Time) (string, string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
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
	entries[found].promptID = promptID
	return entries[found], true
}
