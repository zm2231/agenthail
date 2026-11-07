package surfaces

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// Shaped like a real rollout whose turn stopped mid tool call and was never ended.
const codexCutOffTurn = `{"timestamp":"2026-11-06T15:40:30Z","type":"session_meta","payload":{"id":"thread"}}
{"timestamp":"2026-11-06T15:40:37.058Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1","turn_attribution":{"turn_id":"turn-1","turn_trigger":"exec","parent_turn_id":null}}}
{"timestamp":"2026-11-06T15:40:37.063Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-fixture"}}
{"timestamp":"2026-11-06T15:40:37.067Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Re-review the fix"}]}}
{"timestamp":"2026-11-06T15:40:43.571Z","type":"response_item","payload":{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Reading the diff."}]}}
{"timestamp":"2026-11-06T15:40:48.172Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"call-1","name":"exec","input":"git diff"}}
{"timestamp":"2026-11-06T15:40:48.269Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-1","output":"ok"}}
{"timestamp":"2026-11-06T15:40:48.271Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":120},"last_token_usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}}}
{"timestamp":"2026-11-06T15:43:54.353Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"call-2","name":"exec","input":"go test ./..."}}
{"timestamp":"2026-11-06T15:44:39.998Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2","turn_attribution":{"turn_id":"turn-2","turn_trigger":"exec","parent_turn_id":null}}}
{"timestamp":"2026-11-06T15:44:40.003Z","type":"turn_context","payload":{"turn_id":"turn-2","model":"gpt-fixture"}}
{"timestamp":"2026-11-06T15:44:40.006Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Re-review again"}]}}
{"timestamp":"2026-11-06T15:44:50Z","type":"response_item","payload":{"type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Done."}]}}
{"timestamp":"2026-11-06T15:44:50.1Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":170},"last_token_usage":{"input_tokens":40,"output_tokens":10,"total_tokens":50}}}}
{"timestamp":"2026-11-06T15:44:51Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-2","last_agent_message":"Done.","duration_ms":11000}}
`

func TestOnlyAnOpenCodexTurnWithAKnownIDIsCutOffByTheNextStart(t *testing.T) {
	const (
		startA  = `{"timestamp":"2026-11-06T15:40:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-a"}}` + "\n"
		startA2 = `{"timestamp":"2026-11-06T15:41:30Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-a"}}` + "\n"
		startNo = `{"timestamp":"2026-11-06T15:40:00Z","type":"event_msg","payload":{"type":"task_started"}}` + "\n"
		workA   = `{"timestamp":"2026-11-06T15:40:01Z","type":"event_msg","payload":{"type":"user_message","message":"first"}}
{"timestamp":"2026-11-06T15:40:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"working"}]}}
`
		endA   = `{"timestamp":"2026-11-06T15:40:03Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-a","duration_ms":3000}}` + "\n"
		startB = `{"timestamp":"2026-11-06T15:42:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-b"}}
{"timestamp":"2026-11-06T15:42:01Z","type":"event_msg","payload":{"type":"user_message","message":"second"}}
{"timestamp":"2026-11-06T15:42:02Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-b","duration_ms":2000}}
`
	)
	for _, test := range []struct {
		name    string
		rollout string
		want    []string
	}{
		{"an open turn is cut off", startA + workA + startB, []string{"turn-a interrupted", "turn-b completed"}},
		{"a turn that ended is not cut off again", startA + workA + endA + startB, []string{"turn-a completed", "turn-b completed"}},
		{"a turn whose start was not seen is not cut off", workA + startB, []string{"turn-b completed"}},
		{"a turn without an ID is not cut off", startNo + workA + startB, []string{"turn-b completed"}},
		{"a start repeating the open turn's ID does not cut it off", startA + workA + startA2 + workA + startB, []string{"turn-a interrupted", "turn-b completed"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := timelineFixture(t, `{"timestamp":"2026-11-06T15:39:00Z","type":"session_meta","payload":{"id":"thread"}}`+"\n"+test.rollout)
			read, err := (&Codex{desktopURL: "ws://127.0.0.1:1"}).ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, item := range read.Items {
				if item.Kind == "done" && item.Turn != nil {
					got = append(got, item.TurnID+" "+item.Turn.State)
				}
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("done items %q, want %q", got, test.want)
			}
		})
	}
}

func TestACodexTurnTheNextStartCutOffEndsInterruptedWithItsUsage(t *testing.T) {
	path := timelineFixture(t, codexCutOffTurn)
	read, err := (&Codex{desktopURL: "ws://127.0.0.1:1"}).ReadSession(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop, Transcript: path, Status: surface.StatusIdle}, surface.SessionReadRequest{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var page []surface.TimelineItem
	for _, item := range read.Items {
		if item.Kind == "done" {
			page = append(page, item)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var streamed []surface.StreamEvent
	err = (&Codex{}).Stream(ctx, &surface.Session{ID: "thread", Transcript: path, TranscriptOffset: 0, TranscriptOffsetSet: true}, "", func(event surface.StreamEvent) {
		if event.Kind == "done" {
			if streamed = append(streamed, event); len(streamed) == 2 {
				cancel()
			}
		}
	}, 2*time.Second)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(page) != 2 || len(streamed) != 2 {
		t.Fatalf("page done=%+v streamed done=%+v, want one per turn", page, streamed)
	}
	cut := page[0]
	if cut.TurnID != "turn-1" || cut.Title != "Turn interrupted" || cut.Turn == nil || cut.Turn.State != surface.TurnInterrupted || cut.Turn.Usage == nil || cut.Turn.Usage.OutputTokens != 20 || cut.Turn.Model != "gpt-fixture" {
		t.Fatalf("cut-off turn done=%+v turn=%+v", cut, cut.Turn)
	}
	if next := page[1]; next.TurnID != "turn-2" || next.Turn == nil || next.Turn.State != surface.TurnCompleted || next.Turn.Usage == nil || next.Turn.Usage.OutputTokens != 10 {
		t.Fatalf("next turn done=%+v turn=%+v", next, next.Turn)
	}
	for index, event := range streamed {
		if event.ID != page[index].ID || event.TurnID != page[index].TurnID || event.Turn == nil || event.Turn.State != page[index].Turn.State {
			t.Fatalf("streamed done %d=%+v, page has %+v", index, event, page[index])
		}
	}
}
