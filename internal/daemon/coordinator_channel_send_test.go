package daemon

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zm2231/agenthail/internal/broadcast"
)

func TestAChannelMessageToAGoneLeadNamesTheConversationThatTookIt(t *testing.T) {
	f := newCoordinatorFixture(t)
	if _, err := f.r.CreateChannel("team"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.AddToChannel("team", goneCoordinator); err != nil {
		t.Fatal(err)
	}
	response := serveV1Action(f.handler, `{"action":"channel-send","channel":"team","message":"standup"}`, "channel-gone-lead", f.token)
	var answer struct {
		Recipients []broadcast.Recipient `json:"recipients"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	current := f.current(t)
	if current == goneCoordinator || f.started(t) != 1 {
		t.Fatalf("current=%s started=%d, want one new conversation", current, f.started(t))
	}
	if len(answer.Recipients) != 1 || answer.Recipients[0].SessionID != current || answer.Recipients[0].Outcome != broadcast.OutcomeSent {
		t.Fatalf("recipients=%+v, want the lead reached in conversation %s", answer.Recipients, current)
	}
}
