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

// Held until the session's prompt hook sees them, so both captures name one utterance.
type deliveredWords struct {
	mu        sync.Mutex
	bySession map[string][]deliveredEntry
}

type deliveredEntry struct {
	uuid string
	text string
	at   time.Time
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

func (w *deliveredWords) claim(sessionID, uuid, text string, now time.Time) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	entries := w.bySession[sessionID]
	text = strings.TrimSpace(text)
	found := -1
	for i, entry := range entries {
		if uuid != "" && entry.uuid == uuid {
			found = i
			break
		}
	}
	if found < 0 {
		for i, entry := range entries {
			if entry.text == text && now.Sub(entry.at) <= deliveredTextWindow {
				found = i
				break
			}
		}
	}
	if found < 0 {
		return "", false
	}
	claimed := entries[found].uuid
	entries = append(entries[:found:found], entries[found+1:]...)
	if len(entries) == 0 {
		delete(w.bySession, sessionID)
	} else {
		w.bySession[sessionID] = entries
	}
	return claimed, true
}
