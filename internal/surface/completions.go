package surface

import (
	"context"
	"time"
)

// CompletionReader replays successful completions from a durable provider cursor.
// An empty cursor establishes a baseline without replaying existing history.
// Callers must persist Events and Cursor atomically and retry the old cursor on failure.
type CompletionReader interface {
	ReadCompletions(context.Context, *Session, string) (CompletionBatch, error)
}

// CompletionScanner reads the completions of many sessions in one pass, such
// as one relay scan. The reader it returns may resolve every session's
// transcript from one listing, so it suits a single pass and not a wait for a
// transcript that has yet to appear.
type CompletionScanner interface {
	CompletionScan() CompletionReader
}

type Completion struct {
	CompletedAt time.Time `json:"completedAt,omitempty"`
	ID          string    `json:"id"`
	InputID     string    `json:"inputId,omitempty"`
	Text        string    `json:"text"`
	User        string    `json:"user,omitempty"`
}

type CompletionBatch struct {
	Cursor string
	Events []Completion
}
