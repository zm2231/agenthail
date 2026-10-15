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
