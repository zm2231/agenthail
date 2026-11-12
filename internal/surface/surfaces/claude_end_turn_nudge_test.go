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

func TestTheReplyToTheEndTurnNudgeIsACompletionWhoseInputIsTheNudge(t *testing.T) {
	path, session, cursor := baselineCompletions(t)
	testkit.AppendFile(t, path, stopHookTurn(agenttools.EndTurnNudge)...)
	batch, err := NewClaude(nil, t.TempDir()).ReadCompletions(context.Background(), session, cursor)
	if err != nil || len(batch.Events) != 2 {
		t.Fatalf("events=%+v err=%v", batch.Events, err)
	}
	reply, nudged := batch.Events[0], batch.Events[1]
	if reply.ID != "m1" || reply.Text != "ONE" || reply.User != "say one" {
		t.Fatalf("the turn's reply = %+v", reply)
	}
	if nudged.ID != "m2" || nudged.Text != "Turn closed." || !strings.Contains(nudged.User, agenttools.EndTurnNudge) {
		t.Fatalf("the nudge's reply = %+v, want its input to carry the nudge", nudged)
	}
}
