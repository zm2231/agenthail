package daemon

import (
	_ "embed"
	"encoding/json"
	"strings"
)

type snapshotEvent struct {
	Event  string `json:"event"`
	Family bool   `json:"family"`
	Cache  bool   `json:"cache"`
	Reread bool   `json:"reread"`
	Why    string `json:"why"`
}

//go:embed snapshot_events.json
var snapshotEventsJSON []byte

var snapshotEvents = mustSnapshotEvents(snapshotEventsJSON)

func mustSnapshotEvents(data []byte) []snapshotEvent {
	var events []snapshotEvent
	if err := json.Unmarshal(data, &events); err != nil {
		panic("snapshot_events.json: " + err.Error())
	}
	return events
}

func classifySnapshotEvent(eventType string) (snapshotEvent, bool) {
	for _, event := range snapshotEvents {
		if event.Event == eventType || event.Family && strings.HasPrefix(eventType, event.Event) {
			return event, true
		}
	}
	return snapshotEvent{}, false
}

func changesSnapshot(eventType string) bool {
	event, _ := classifySnapshotEvent(eventType)
	return event.Cache
}
