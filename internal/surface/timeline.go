package surface

import (
	"context"
	"errors"
	"time"
)

var ErrTranscriptUnavailable = errors.New("session transcript unavailable")
var ErrTranscriptChanged = errors.New("session transcript was replaced")
var ErrTranscriptBacklog = errors.New("session transcript holds more than a forward read takes")

// TimelineItem preserves the ordered, user-visible contents of an agent transcript.
type TimelineItem struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Role      string `json:"role,omitempty"`
	Origin    string `json:"origin,omitempty"`
	Sender    string `json:"sender,omitempty"`
	Title     string `json:"title"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
	CallID    string `json:"callId,omitempty"`
	TurnID    string `json:"turnId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	Status    string `json:"status,omitempty"`
	// ResetsAt is when a usage limit a notice reports lifts, in RFC 3339.
	ResetsAt         string         `json:"resetsAt,omitempty"`
	Truncated        bool           `json:"truncated"`
	TruncationReason string         `json:"truncationReason,omitempty"`
	BodyRef          string         `json:"bodyRef,omitempty"`
	Attachment       *Attachment    `json:"attachment,omitempty"`
	Files            []AttachedFile `json:"files,omitempty"`
	Turn             *TurnMetadata  `json:"turn,omitempty"`
	Agent            *AgentMessage  `json:"agent,omitempty"`
}

// AttachedFile is a file a person sent with a message that the agent received
// as a stored path rather than as image input. Path is on the host that owns
// the session.
type AttachedFile struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes,omitempty"`
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
	JournalSeq            uint64            `json:"journalSeq,omitempty"`
	TranscriptOffset      int64             `json:"-"`
	TranscriptOffsetSet   bool              `json:"-"`
	TranscriptIdentity    string            `json:"-"`
	CodexPendingEventUser bool              `json:"-"`
	CodexPendingEventTurn string            `json:"-"`
	CodexCurrentTurnID    string            `json:"-"`
	ClaudeClosingTurn     ClaudeClosingTurn `json:"-"`
	Items                 []TimelineItem    `json:"items"`
	Exchanges             []Exchange        `json:"exchanges"`
	Reply                 *ReplyResult      `json:"reply,omitempty"`
	NextBefore            int64             `json:"nextBefore"`
	Source                string            `json:"source"`
	Truncated             bool              `json:"truncated"`
	UnavailableReason     string            `json:"unavailableReason,omitempty"`
	Warning               string            `json:"warning,omitempty"`
}

type SessionReader interface {
	ReadSession(context.Context, *Session, SessionReadRequest) (*SessionReadResult, error)
}

type AttachmentReader interface {
	ReadAttachment(context.Context, *Session, string) (*Attachment, []byte, error)
}

func ReadSession(ctx context.Context, adapter Surface, session *Session, request SessionReadRequest) (*SessionReadResult, error) {
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
		if result.Exchanges[index].Turn == nil {
			state := TurnUnknown
			if index == len(result.Exchanges)-1 && session != nil && session.Status == StatusBusy {
				state = TurnRunning
			}
			result.Exchanges[index].Turn = NewTurnMetadata(state, TurnMeasure{})
		}
	}
	if result.Reply == nil {
		result.Reply = latestReply(session, result.Exchanges, result.Source)
	}
	return result
}

func latestReply(session *Session, exchanges []Exchange, source string) *ReplyResult {
	for index := len(exchanges) - 1; index >= 0; index-- {
		if exchanges[index].Assistant == "" || exchanges[index].Command {
			continue
		}
		done := session == nil || session.Status != StatusBusy
		return &ReplyResult{Text: exchanges[index].Assistant, UserText: exchanges[index].User, Done: done, Source: source}
	}
	return &ReplyResult{Done: false, Source: source}
}

// MaxExchangePage is the most exchanges one session page returns.
const MaxExchangePage = 50

// IsPromptItem reports whether item opens a turn: a message from the person or
// from another agent, or a prompt the provider injected on the person's
// behalf.
func IsPromptItem(item TimelineItem) bool {
	if item.Text == "" {
		return false
	}
	switch item.Kind {
	case "message", "text":
		return item.Role == "user" || item.Role == "peer"
	case "event":
		return item.Origin != ""
	}
	return false
}

func promptOrigin(item TimelineItem) string {
	if item.Role == "peer" {
		return "peer"
	}
	return item.Origin
}

func isReplyItem(item TimelineItem) bool {
	switch item.Kind {
	case "message", "assistant":
		return item.Role == "assistant" && item.Text != ""
	case "text":
		return (item.Role == "assistant" || item.Role == "") && item.Text != ""
	}
	return false
}

// ExchangesFromItems groups ordered timeline items into turns. An exchange is a
// prompt and the last assistant message of its turn. A prompt carrying the
// turn ID of the turn in progress was injected into it and stays in it; a done
// item ends its turn. A reply from another turn, or before any prompt, forms an
// exchange without a prompt. An item without a turn ID belongs to the open
// turn, because journals written before turns were recorded have none.
func ExchangesFromItems(items []TimelineItem, source string) []Exchange {
	exchanges, _ := groupExchanges(items, source)
	return exchanges
}

// ExchangeStarts returns the index of the first item of each exchange, so a
// page can begin exactly at an exchange. An exchange begins with the earliest
// item of its turn after the previous turn's end, since a turn can record
// items, such as the instructions a provider injects, before the prompt that
// opens it, and a provider can reuse a turn ID after that turn ended.
func ExchangeStarts(items []TimelineItem) []int {
	_, starts := groupExchanges(items, "")
	for index, start := range starts {
		floor := 0
		if index > 0 {
			floor = starts[index-1] + 1
		}
		turn := items[start].TurnID
		for turn != "" && start > floor && items[start-1].TurnID == turn && items[start-1].Kind != "done" {
			start--
		}
		starts[index] = start
	}
	return starts
}

func groupExchanges(items []TimelineItem, source string) ([]Exchange, []int) {
	exchanges := []Exchange{}
	starts := []int{}
	turn, open := "", false
	// command is a command run outside a turn, which with its output makes an
	// exchange of its own. A command that prints nothing makes none.
	command := -1
	for index, item := range items {
		at, _ := time.Parse(time.RFC3339Nano, item.Timestamp)
		if item.Kind == "event" && (item.Title == TimelineCommand || item.Title == TimelineLocalCommand) && !open && item.Text != "" {
			if item.Title == TimelineCommand {
				command = index
				continue
			}
			exchange, start := Exchange{Assistant: item.Text, Timestamp: at, Source: source, Command: true}, index
			if command >= 0 {
				exchange.User, start = items[command].Text, command
				if asked, err := time.Parse(time.RFC3339Nano, items[command].Timestamp); err == nil {
					exchange.Timestamp = asked
				}
			}
			exchanges = append(exchanges, exchange)
			starts = append(starts, start)
			command = -1
			continue
		}
		command = -1
		if item.Kind == "done" {
			if open && (item.TurnID == "" || item.TurnID == turn) {
				open = false
				ended := item.Turn
				if ended == nil {
					ended = NewTurnMetadata(TurnStateOfDone(item.Status), TurnMeasure{})
				}
				exchanges[len(exchanges)-1].Turn = ended
			}
			continue
		}
		if IsPromptItem(item) {
			if open && turn != "" && item.TurnID == turn {
				last := &exchanges[len(exchanges)-1]
				switch {
				case last.User == "":
					last.User, last.Origin, last.Sender = item.Text, promptOrigin(item), item.Sender
				case last.Origin == promptOrigin(item) && last.Sender == item.Sender:
					last.User += "\n\n" + item.Text
				}
				continue
			}
			exchanges = append(exchanges, Exchange{User: item.Text, Origin: promptOrigin(item), Sender: item.Sender, Timestamp: at, Source: source})
			starts = append(starts, index)
			turn, open = item.TurnID, true
			continue
		}
		if !isReplyItem(item) {
			continue
		}
		if !open || (turn != "" && item.TurnID != "" && item.TurnID != turn) {
			exchanges = append(exchanges, Exchange{Timestamp: at, Source: source})
			starts = append(starts, index)
			turn, open = item.TurnID, true
		}
		if turn == "" {
			turn = item.TurnID
		}
		exchanges[len(exchanges)-1].Assistant = item.Text
	}
	return exchanges, starts
}

// TimelineCommand and TimelineLocalCommand title the events for a slash
// command Claude Code ran itself rather than sending it to the model, and
// for what it printed.
const (
	TimelineCommand      = "Command"
	TimelineLocalCommand = "Local command"
)

// TimelineUsageLimit is the status of a notice that Claude refused a turn
// because a usage limit was reached.
const TimelineUsageLimit = "usage_limit"
