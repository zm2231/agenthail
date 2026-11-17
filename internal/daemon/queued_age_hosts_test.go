package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAStaleMessageFromAPairedHostSaysWhenItWasSent(t *testing.T) {
	local, remote, record := widgetOnTwoHosts(t)
	host, _ := local.daemon.pairedHost(record.ID)
	sent := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339Nano)
	for key, action := range map[string]map[string]any{
		"post": {"action": "channel-deliver", "projectId": "repo:github.com/example/widget", "message": "standup moved to noon", "sentAt": sent},
		"send": {"action": "send", "sessionId": "remote-c", "message": "the build is green", "sentAt": sent},
	} {
		body, _ := json.Marshal(action)
		response, err := host.client.Do(context.Background(), http.MethodPost, "/api/v1/actions", nil, body, http.Header{actionIdempotencyHeader: []string{"stale-" + key}})
		if err != nil {
			t.Fatal(err)
		}
		if answer, err := readHostAnswer(host, response); err != nil || answer.status != http.StatusOK {
			t.Fatalf("%s: status=%d err=%v payload=%s", key, answer.status, err, answer.payload)
		}
	}
	marked := map[string]int{}
	for _, message := range remote.fake.delivered() {
		for _, text := range []string{"standup moved to noon", "the build is green"} {
			if strings.Contains(message, "(sent 30 min ago) "+text) {
				marked[text]++
			}
		}
	}
	if marked["standup moved to noon"] != 2 || marked["the build is green"] != 1 {
		t.Fatalf("delivered=%q, want the post to both members and the send each told they are 30 minutes old", remote.fake.delivered())
	}
}
