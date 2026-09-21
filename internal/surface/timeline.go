package surface

import (
	"context"
	"time"
)

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

type SessionReadRequest struct {
	Limit  int
	Before int64
}

type SessionReadResult struct {
	Items             []TimelineItem `json:"items"`
	Exchanges         []Exchange     `json:"exchanges"`
	Reply             *ReplyResult   `json:"reply,omitempty"`
	NextBefore        int64          `json:"nextBefore"`
	Source            string         `json:"source"`
	Truncated         bool           `json:"truncated"`
	UnavailableReason string         `json:"unavailableReason,omitempty"`
}

type SessionReader interface {
	ReadSession(context.Context, *Session, SessionReadRequest) (*SessionReadResult, error)
}

func ReadSession(ctx context.Context, adapter Surface, session *Session, request SessionReadRequest) (*SessionReadResult, error) {
	if request.Limit < 1 {
		request.Limit = 1
	}
	if reader, ok := adapter.(SessionReader); ok {
		return reader.ReadSession(ctx, session, request)
	}
	if provider, ok := adapter.(TimelineProvider); ok {
		timeline, err := provider.Timeline(ctx, session, request.Before)
		if err != nil {
			return nil, err
		}
		return SessionReadFromTimeline(session, timeline, request.Limit), nil
	}
	exchanges, err := adapter.Tail(ctx, session, request.Limit)
	if err != nil {
		return nil, err
	}
	source := string(adapter.Name()) + "-remote"
	if len(exchanges) > 0 && exchanges[len(exchanges)-1].Source != "" {
		source = exchanges[len(exchanges)-1].Source
	}
	result := &SessionReadResult{Exchanges: exchanges, Items: []TimelineItem{}, Source: source}
	for index := range result.Exchanges {
		if result.Exchanges[index].Source == "" {
			result.Exchanges[index].Source = result.Source
		}
	}
	result.Reply = latestReply(session, result.Exchanges, result.Source)
	return result, nil
}

func SessionReadFromTimeline(session *Session, timeline *SessionTimeline, limit int) *SessionReadResult {
	if timeline == nil {
		return &SessionReadResult{Items: []TimelineItem{}, Exchanges: []Exchange{}}
	}
	exchanges := exchangesFromTimeline(timeline.Items)
	if limit > 0 && len(exchanges) > limit {
		exchanges = exchanges[len(exchanges)-limit:]
	}
	for index := range exchanges {
		exchanges[index].Source = timeline.Source
	}
	return &SessionReadResult{
		Items:             timeline.Items,
		Exchanges:         exchanges,
		Reply:             latestReply(session, exchanges, timeline.Source),
		NextBefore:        timeline.NextBefore,
		Source:            timeline.Source,
		Truncated:         timeline.Truncated,
		UnavailableReason: timeline.UnavailableReason,
	}
}

func exchangesFromTimeline(items []TimelineItem) []Exchange {
	exchanges := make([]Exchange, 0)
	for _, item := range items {
		if item.Kind != "message" || item.Text == "" {
			continue
		}
		timestamp, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
		switch item.Role {
		case "user":
			exchanges = append(exchanges, Exchange{User: item.Text, Timestamp: timestamp})
		case "assistant":
			if len(exchanges) == 0 || exchanges[len(exchanges)-1].Assistant != "" {
				exchanges = append(exchanges, Exchange{Assistant: item.Text, Timestamp: timestamp})
			} else {
				exchanges[len(exchanges)-1].Assistant = item.Text
				if exchanges[len(exchanges)-1].Timestamp.IsZero() {
					exchanges[len(exchanges)-1].Timestamp = timestamp
				}
			}
		}
	}
	return exchanges
}

func latestReply(session *Session, exchanges []Exchange, source string) *ReplyResult {
	for index := len(exchanges) - 1; index >= 0; index-- {
		if exchanges[index].Assistant == "" {
			continue
		}
		done := session == nil || session.Status != StatusBusy
		return &ReplyResult{Text: exchanges[index].Assistant, UserText: exchanges[index].User, Done: done, Source: source}
	}
	return &ReplyResult{Done: false, Source: source}
}
