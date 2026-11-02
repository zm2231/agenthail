package ledger

import (
	"encoding/json"
	"time"
)

type WireEvent struct {
	Seq            int64           `json:"seq"`
	Type           EventType       `json:"type"`
	Payload        json.RawMessage `json:"payload"`
	ActorSessionID string          `json:"actorSessionId,omitempty"`
	HostID         string          `json:"hostId"`
	OriginSeq      int64           `json:"originSeq"`
	Ref            string          `json:"ref,omitempty"`
	CreatedAt      string          `json:"createdAt"`
}

func ToWire(events []Event) []WireEvent {
	out := make([]WireEvent, len(events))
	for i, e := range events {
		stamp := e.Stamp
		if stamp == "" {
			stamp = e.CreatedAt.UTC().Format(eventTimeLayout)
		}
		out[i] = WireEvent{Seq: e.Seq, Type: e.Type, Payload: json.RawMessage(e.Payload), ActorSessionID: e.ActorSessionID,
			HostID: e.ActorHostID, OriginSeq: e.OriginSeq, Ref: e.Ref, CreatedAt: stamp}
	}
	return out
}

func FromWire(projectKey string, wire []WireEvent) []Event {
	out := make([]Event, len(wire))
	for i, w := range wire {
		created, _ := time.Parse(eventTimeLayout, w.CreatedAt)
		out[i] = Event{Seq: w.Seq, ProjectKey: projectKey, Type: w.Type, Payload: []byte(w.Payload), ActorSessionID: w.ActorSessionID,
			ActorHostID: w.HostID, OriginSeq: w.OriginSeq, Ref: w.Ref, CreatedAt: created, Stamp: w.CreatedAt}
	}
	return out
}
