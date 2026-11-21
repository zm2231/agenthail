package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAnAnswerToASettledDeliveryAskSaysHowItWasSettled(t *testing.T) {
	for _, first := range []string{"retry", "delete", "dismiss"} {
		t.Run(first, func(t *testing.T) {
			d, r, _, _, _ := daemonFixture(t)
			queueID, _, err := r.QueueDeliveryWithIntent("to", "never arrives", "", surface.SendOptions{SourceSessionID: surface.PersonSessionID})
			if err != nil {
				t.Fatal(err)
			}
			if item, err := r.ClaimNextMessage("to", time.Now()); err != nil || item == nil || item.ID != queueID {
				t.Fatalf("item=%+v err=%v", item, err)
			}
			if err := r.DeadLetterMessage(queueID, errors.New("delivery rejected [target missing]: gone")); err != nil {
				t.Fatal(err)
			}
			asks := openDeliveryAsks(t, r)
			if len(asks) != 1 {
				t.Fatalf("asks=%+v", asks)
			}
			handler := dashboardRouter(d)
			if response := postActionAs(t, handler, "secret", answerBody(asks[0], first)); response.Code != http.StatusOK {
				t.Fatalf("%s status=%d body=%s", first, response.Code, response.Body.String())
			}
			late := postActionAs(t, handler, "secret", answerBody(asks[0], "delete"))
			var reply struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
				Settled string `json:"settled"`
			}
			if err := json.Unmarshal(late.Body.Bytes(), &reply); err != nil || late.Code != http.StatusConflict || reply.Error.Code != "stale" || reply.Settled != first {
				t.Fatalf("late delete status=%d body=%s, want stale and settled %q", late.Code, late.Body.String(), first)
			}
		})
	}
}
