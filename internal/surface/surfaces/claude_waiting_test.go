package surfaces

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestClaudeObserveReportsUnansweredQuestionUntilItsToolResult(t *testing.T) {
	path := writeTranscript(t, `
{"type":"user","uuid":"prompt","timestamp":"2026-10-18T08:14:31Z","message":{"content":"Plan the release"}}
{"type":"assistant","uuid":"thinking","timestamp":"2026-10-18T08:14:37Z","message":{"id":"msg-1","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"ask first"}]}}
{"type":"assistant","uuid":"ask","timestamp":"2026-10-18T08:14:41Z","message":{"id":"msg-1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_question","name":"AskUserQuestion","input":{"questions":[{"question":"Which branch?"}]}}]}}
{"type":"queue-operation","timestamp":"2026-10-18T08:27:43Z"}
{"type":"system","subtype":"informational","timestamp":"2026-10-18T09:18:59Z"}
{"type":"attachment","timestamp":"2026-10-18T08:14:41Z"}`)
	claude := NewClaude("", t.TempDir())
	session := &surface.Session{ID: "bridge", Surface: surface.KindClaude, Status: surface.StatusUnknown, Transcript: path}
	observation, err := claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.ActiveTurnID != "prompt" {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	want := surface.Waiting{Kind: surface.WaitingQuestion, ID: "toolu_question", Since: time.Date(2026, 10, 18, 8, 14, 41, 0, time.UTC)}
	if observation.WaitingOn == nil || *observation.WaitingOn != want {
		t.Fatalf("waiting=%+v", observation.WaitingOn)
	}
	appendTestTranscript(t, path, `{"type":"user","uuid":"answer","timestamp":"2026-10-18T20:18:07Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_question","content":"Your questions have been answered"}]}}`)
	observation, err = claude.Observe(context.Background(), session)
	if err != nil || observation.Status != surface.StatusBusy || observation.WaitingOn != nil {
		t.Fatalf("answered observation=%+v err=%v", observation, err)
	}
}

func TestClaudeObserveWaitingKinds(t *testing.T) {
	for _, test := range []struct {
		name       string
		transcript string
		want       *surface.Waiting
	}{
		{
			name: "plan approval",
			transcript: `
{"type":"user","uuid":"prompt","message":{"content":"Plan it"}}
{"type":"assistant","uuid":"plan","message":{"id":"m","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_plan","name":"ExitPlanMode","input":{"plan":"steps"}}]}}`,
			want: &surface.Waiting{Kind: surface.WaitingPlan, ID: "toolu_plan"},
		},
		{
			name: "ordinary tool use is busy, not waiting",
			transcript: `
{"type":"user","uuid":"prompt","message":{"content":"Build"}}
{"type":"assistant","uuid":"bash","message":{"id":"m","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"make"}}]}}`,
		},
		{
			name: "interrupted question clears",
			transcript: `
{"type":"user","uuid":"prompt","message":{"content":"Plan it"}}
{"type":"assistant","uuid":"ask","message":{"id":"m","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_q","name":"AskUserQuestion","input":{}}]}}
{"type":"user","uuid":"stop","message":{"content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`,
		},
		{
			name: "a new prompt replaces an unanswered question",
			transcript: `
{"type":"user","uuid":"prompt","message":{"content":"Plan it"}}
{"type":"assistant","uuid":"ask","message":{"id":"m","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_q","name":"AskUserQuestion","input":{}}]}}
{"type":"user","uuid":"next","message":{"content":"Never mind, do this instead"}}`,
		},
		{
			name: "finished turn is not waiting",
			transcript: `
{"type":"user","uuid":"prompt","message":{"content":"Plan it"}}
{"type":"assistant","uuid":"ask","message":{"id":"m","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_q","name":"AskUserQuestion","input":{}}]}}
{"type":"user","uuid":"answer","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_q","content":"main"}]}}
{"type":"assistant","uuid":"done","message":{"id":"m2","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			claude := NewClaude("", t.TempDir())
			session := &surface.Session{ID: "bridge", Surface: surface.KindClaude, Status: surface.StatusUnknown, Transcript: writeTranscript(t, test.transcript)}
			observation, err := claude.Observe(context.Background(), session)
			if err != nil {
				t.Fatal(err)
			}
			if !surface.SameWaiting(observation.WaitingOn, test.want) {
				t.Fatalf("waiting=%+v want=%+v observation=%+v", observation.WaitingOn, test.want, observation)
			}
		})
	}
}
