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

// Held until the session's prompt hook sees them, so both captures name one
// utterance. A claimed delivery stays bound to the prompt that claimed it,
// since every copy of the hook reports that prompt.
type deliveredWords struct {
	mu        sync.Mutex
	bySession map[string][]deliveredEntry
}

type deliveredEntry struct {
	uuid     string
	text     string
	at       time.Time
	promptID string
}

func (w *deliveredWords) add(sessionID, uuid, text string, at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.bySession == nil {
		w.bySession = map[string][]deliveredEntry{}
	}
	entries := append(w.bySession[sessionID], deliveredEntry{uuid: uuid, text: strings.TrimSpace(text), at: at})
	if len(entries) > deliveredPerSession {
		entries = entries[len(entries)-deliveredPerSession:]
	}
	w.bySession[sessionID] = entries
}

func (w *deliveredWords) claim(sessionID, promptID, uuid, text string, now time.Time) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entries := w.bySession[sessionID]
	text = strings.TrimSpace(text)
	found := -1
	for i, entry := range entries {
		if entry.promptID == promptID || entry.promptID == "" && uuid != "" && entry.uuid == uuid {
			found = i
			break
		}
	}
	if found < 0 {
		for i, entry := range entries {
			if entry.promptID == "" && entry.text == text && now.Sub(entry.at) <= deliveredTextWindow {
				found = i
				break
			}
		}
	}
	if found < 0 {
		return "", false
	}
	entries[found].promptID = promptID
	return entries[found].uuid, true
}
