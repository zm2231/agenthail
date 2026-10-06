package surface

import (
	"context"
	"errors"
)

var ErrTranscriptUnavailable = errors.New("session transcript unavailable")

// TimelineItem preserves the ordered, user-visible contents of an agent transcript.
type TimelineItem struct {
	ID               string      `json:"id"`
	Kind             string      `json:"kind"`
	Role             string      `json:"role,omitempty"`
	Origin           string      `json:"origin,omitempty"`
	Sender           string      `json:"sender,omitempty"`
	Title            string      `json:"title"`
	Text             string      `json:"text"`
	Timestamp        string      `json:"timestamp,omitempty"`
	CallID           string      `json:"callId,omitempty"`
	TurnID           string      `json:"turnId,omitempty"`
	Status           string      `json:"status,omitempty"`
	Truncated        bool        `json:"truncated"`
	TruncationReason string      `json:"truncationReason,omitempty"`
	BodyRef          string      `json:"bodyRef,omitempty"`
	Attachment       *Attachment `json:"attachment,omitempty"`
}

type Attachment struct {
	ID        string `json:"id"`
	MediaType string `json:"mediaType"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
}

type SessionTimeline struct {
	NextBefore        int64          `json:"nextBefore"`
	Items             []TimelineItem `json:"items"`
	Source            string         `json:"source"`
	Truncated         bool           `json:"truncated"`
	UnavailableReason string         `json:"unavailableReason,omitempty"`
}

type SessionReadRequest struct {
	Limit  int
	Before int64
}

type SessionReadResult struct {
	JournalSeq            uint64         `json:"journalSeq,omitempty"`
	TranscriptOffset      int64          `json:"-"`
	TranscriptOffsetSet   bool           `json:"-"`
	TranscriptIdentity    string         `json:"-"`
	CodexPendingEventUser bool           `json:"-"`
	CodexPendingEventTurn string         `json:"-"`
	CodexCurrentTurnID    string         `json:"-"`
	Items                 []TimelineItem `json:"items"`
	Exchanges             []Exchange     `json:"exchanges"`
	Reply                 *ReplyResult   `json:"reply,omitempty"`
	NextBefore            int64          `json:"nextBefore"`
	Source                string         `json:"source"`
	Truncated             bool           `json:"truncated"`
	UnavailableReason     string         `json:"unavailableReason,omitempty"`
	Warning               string         `json:"warning,omitempty"`
}

type SessionReader interface {
	ReadSession(context.Context, *Session, SessionReadRequest) (*SessionReadResult, error)
}

type AttachmentReader interface {
	ReadAttachment(context.Context, *Session, string) (*Attachment, []byte, error)
}

func ReadSession(ctx context.Context, adapter Surface, session *Session, request SessionReadRequest) (*SessionReadResult, error) {
	if err := ValidateRuntimeTransport(session); err != nil {
		return nil, err
	}
	if request.Limit < 1 {
		request.Limit = 1
	}
	if reader, ok := adapter.(SessionReader); ok {
		return reader.ReadSession(ctx, session, request)
	}
	exchanges, err := adapter.Tail(ctx, session, request.Limit)
	if err != nil {
		return nil, err
	}
	source := string(adapter.Name()) + "-remote"
	if len(exchanges) > 0 && exchanges[len(exchanges)-1].Source != "" {
		source = exchanges[len(exchanges)-1].Source
	}
	return BoundSessionRead(session, &SessionReadResult{Exchanges: exchanges, Items: []TimelineItem{}, Source: source}), nil
}

// BoundSessionRead stamps the exchange source and derives the latest reply when the reader did not.
func BoundSessionRead(session *Session, result *SessionReadResult) *SessionReadResult {
	if result.Items == nil {
		result.Items = []TimelineItem{}
	}
	if result.Exchanges == nil {
		result.Exchanges = []Exchange{}
	}
	for index := range result.Exchanges {
		if result.Exchanges[index].Source == "" {
			result.Exchanges[index].Source = result.Source
		}
	}
	if result.Reply == nil {
		result.Reply = latestReply(session, result.Exchanges, result.Source)
	}
	return result
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
