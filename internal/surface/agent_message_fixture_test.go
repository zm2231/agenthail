package surface

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestAgentMessageFixtureMatchesTheDaemon(t *testing.T) {
	data, err := os.ReadFile("../../scripts/fixtures/agent-messages-timeline.json")
	if err != nil {
		t.Fatal(err)
	}
	var items []TimelineItem
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		want := item.Agent
		item.Agent = nil
		if got := PresentTimelineItem(item).Agent; !reflect.DeepEqual(got, want) {
			gotJSON, _ := json.Marshal(got)
			t.Errorf("item %s: daemon gives %s", item.ID, gotJSON)
		}
	}
}
