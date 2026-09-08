package surface

import "context"

// TimelineItem preserves the ordered, user-visible contents of an agent transcript.
type TimelineItem struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Role      string `json:"role,omitempty"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
	CallID    string `json:"callId,omitempty"`
	Status    string `json:"status,omitempty"`
	Truncated bool   `json:"truncated"`
}

type SessionTimeline struct {
	NextBefore        int64          `json:"nextBefore"`
	Items             []TimelineItem `json:"items"`
	Source            string         `json:"source"`
	Truncated         bool           `json:"truncated"`
	UnavailableReason string         `json:"unavailableReason,omitempty"`
}

type TimelineProvider interface {
	Timeline(context.Context, *Session, int64) (*SessionTimeline, error)
}
