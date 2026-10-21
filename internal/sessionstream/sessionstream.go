package sessionstream

import (
	"encoding/json"
	"fmt"
)

// Event is the consumer-facing projection of one journal payload. It keeps
// journal identity and mutation data so a consumer can replay without
// duplicating an item or treating another turn as its own.
type Event struct {
	Seq              uint64
	ItemID           string
	ProviderKey      string
	Version          uint64
	Operation        string
	Kind             string
	TurnID           string
	CallID           string
	Role             string
	Status           string
	ResetsAt         string
	Body             string
	Reason           string
	Final            bool
	Truncated        bool
	TruncationReason string
	BodyRef          string
}

type Subscription struct {
	Cursor uint64
	Events <-chan Event
	Cancel func()
}

func DecodePayload(seq uint64, payload []byte) (Event, error) {
	var event struct {
		ItemID           string `json:"itemId"`
		ProviderKey      string `json:"providerKey"`
		Version          uint64 `json:"version"`
		Operation        string `json:"op"`
		Kind             string `json:"kind"`
		TurnID           string `json:"turnId"`
		CallID           string `json:"callId"`
		Role             string `json:"role"`
		Status           string `json:"status"`
		ResetsAt         string `json:"resetsAt"`
		Body             string `json:"body"`
		Reason           string `json:"reason"`
		Final            bool   `json:"final"`
		Truncated        bool   `json:"truncated"`
		TruncationReason string `json:"truncationReason"`
		BodyRef          string `json:"bodyRef"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return Event{}, fmt.Errorf("decode session journal event: %w", err)
	}
	return Event{Seq: seq, ItemID: event.ItemID, ProviderKey: event.ProviderKey, Version: event.Version, Operation: event.Operation, Kind: event.Kind, TurnID: event.TurnID, CallID: event.CallID, Role: event.Role, Status: event.Status, ResetsAt: event.ResetsAt, Body: event.Body, Reason: event.Reason, Final: event.Final, Truncated: event.Truncated, TruncationReason: event.TruncationReason, BodyRef: event.BodyRef}, nil
}

func (e Event) Terminal() bool {
	return e.Kind == "done" || e.Kind == "source-error"
}

func (e Event) Failed() bool {
	if e.Kind == "source-error" {
		return true
	}
	switch e.Status {
	case "failed", "error", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

// FailureReason is why a failed event failed: its source reason, the body a
// failed turn's end carries, such as an API refusal, else its status.
func (e Event) FailureReason() string {
	for _, reason := range []string{e.Reason, e.Body, e.Status} {
		if reason != "" {
			return reason
		}
	}
	return "terminal failure"
}

func (e Event) SpokenAssistantContent() bool {
	if e.Role != "assistant" {
		return false
	}
	switch e.Kind {
	case "message", "text", "assistant":
		return true
	default:
		return false
	}
}
