package surfaces

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/testkit"
)

func stopHookTurn(feedback string) []string {
	quoted, _ := json.Marshal("Stop hook feedback:\n" + feedback)
	reason, _ := json.Marshal(feedback)
	return []string{
		promptRecord,
		textRecord,
		`{"type":"attachment","timestamp":"2026-10-18T22:07:54.25Z"}` + "\n",
		`{"type":"user","uuid":"f1","isMeta":true,"timestamp":"2026-10-18T22:07:54.3Z","message":{"role":"user","content":` + string(quoted) + `}}` + "\n",
		`{"type":"system","subtype":"stop_hook_summary","hookCount":1,"hookErrors":[` + string(reason) + `],"preventedContinuation":false,"timestamp":"2026-10-18T22:07:54.3Z"}` + "\n",
		`{"type":"assistant","uuid":"a2","timestamp":"2026-10-18T22:07:55Z","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"Turn closed."}]}}` + "\n",
		durationRecord,
	}
}

func TestTheReplyToTheEndTurnNudgeIsMarkedAsAnsweringIt(t *testing.T) {
	for _, test := range []struct {
		name     string
		feedback string
		nudge    bool
	}{
		{"the nudge alone", agenttools.EndTurnNudge, true},
		{"the nudge with an ask reminder", agenttools.EndTurnNudge + "\nYou asked a question in prose; put it to your lead with agenthail_ask.", true},
		{"a user's own Stop hook", "fix the type errors before stopping", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, session, cursor := baselineCompletions(t)
			testkit.AppendFile(t, path, stopHookTurn(test.feedback)...)
			batch, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, cursor)
			if err != nil || len(batch.Events) != 2 {
				t.Fatalf("events=%+v err=%v", batch.Events, err)
			}
			reply, followUp := batch.Events[0], batch.Events[1]
			if reply.ID != "m1" || reply.Text != "ONE" || reply.User != "say one" || reply.EndTurnNudge {
				t.Fatalf("the turn's reply = %+v", reply)
			}
			if followUp.ID != "m2" || followUp.Text != "Turn closed." || !strings.Contains(followUp.User, test.feedback) || followUp.EndTurnNudge != test.nudge {
				t.Fatalf("the follow-up = %+v, want EndTurnNudge %v", followUp, test.nudge)
			}
		})
	}
}

func TestAPromptQuotingTheEndTurnNudgeIsNotMarked(t *testing.T) {
	path, session, cursor := baselineCompletions(t)
	quoted, _ := json.Marshal("What does this mean? " + agenttools.EndTurnNudge)
	testkit.AppendFile(t, path,
		`{"type":"user","uuid":"u2","timestamp":"2026-10-18T22:08:00Z","message":{"content":`+string(quoted)+`}}`+"\n",
		`{"type":"assistant","uuid":"a3","timestamp":"2026-10-18T22:08:01Z","message":{"id":"m3","stop_reason":"end_turn","content":[{"type":"text","text":"It asks me to close the turn."}]}}`+"\n",
		`{"type":"system","subtype":"turn_duration","timestamp":"2026-10-18T22:08:01.1Z"}`+"\n",
	)
	batch, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, cursor)
	if err != nil || len(batch.Events) != 1 {
		t.Fatalf("events=%+v err=%v", batch.Events, err)
	}
	if got := batch.Events[0]; got.ID != "m3" || !strings.Contains(got.User, agenttools.EndTurnNudge) || got.EndTurnNudge {
		t.Fatalf("the reply = %+v, want it unmarked", got)
	}
}
