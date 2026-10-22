package surface

import (
	"reflect"
	"testing"
)

func TestBoundSessionReadStampsSourceAndKeepsReaderReply(t *testing.T) {
	session := &Session{Status: StatusIdle}
	result := BoundSessionRead(session, &SessionReadResult{
		Source:    "local-transcript",
		Exchanges: []Exchange{{User: "first", Assistant: "one"}, {User: "second", Assistant: "two"}},
	})
	if len(result.Exchanges) != 2 || result.Exchanges[0].Source != "local-transcript" || len(result.Items) != 0 {
		t.Fatalf("result=%+v", result)
	}
	if result.Reply == nil || result.Reply.Text != "two" || !result.Reply.Done || result.Reply.Source != "local-transcript" {
		t.Fatalf("reply=%+v", result.Reply)
	}
	busy := BoundSessionRead(&Session{Status: StatusBusy}, &SessionReadResult{Source: "rpc", Exchanges: []Exchange{{Assistant: "checking"}}})
	if busy.Reply == nil || busy.Reply.Done {
		t.Fatalf("reply=%+v", busy.Reply)
	}
	preset := &ReplyResult{Text: "partial", Done: false, Source: "local-transcript"}
	kept := BoundSessionRead(&Session{Status: StatusIdle}, &SessionReadResult{Source: "local-transcript", Exchanges: []Exchange{{Assistant: "partial"}}, Reply: preset})
	if kept.Reply != preset || kept.Reply.Done {
		t.Fatalf("reader reply was replaced: %+v", kept.Reply)
	}
}

func TestExchangesFollowTurnMarkersAndLabelInjectedPrompts(t *testing.T) {
	items := []TimelineItem{
		{Kind: "message", Role: "user", Text: "first", TurnID: "t1", Timestamp: "2026-10-19T10:00:00Z"},
		{Kind: "message", Role: "assistant", Text: "working", TurnID: "t1"},
		{Kind: "message", Role: "peer", Origin: "peer", Sender: "reviewer", Text: "steer", TurnID: "t1"},
		{Kind: "message", Role: "assistant", Text: "one", TurnID: "t1"},
		{Kind: "done", TurnID: "t1"},
		{Kind: "message", Role: "peer", Origin: "peer", Sender: "reviewer", Text: "after the close", TurnID: "t1", Timestamp: "2026-10-19T10:00:30Z"},
		{Kind: "message", Role: "assistant", Text: "answered", TurnID: "t1"},
		{Kind: "event", Role: "system", Origin: "task-notification", Text: "build finished", TurnID: "t2", Timestamp: "2026-10-19T10:01:00Z"},
		{Kind: "message", Role: "assistant", Text: "two", TurnID: "t2"},
		{Kind: "message", Role: "peer", Origin: "peer", Sender: "collab", Text: "next task", TurnID: "t3", Timestamp: "2026-10-19T10:02:00Z"},
		{Kind: "message", Role: "assistant", Text: "three", TurnID: "t3"},
		{Kind: "message", Role: "user", Text: "<app context>", TurnID: "t4", Timestamp: "2026-10-19T10:03:00Z"},
		{Kind: "message", Role: "user", Text: "how does it work?", TurnID: "t4"},
		{Kind: "message", Role: "assistant", Text: "like this", TurnID: "t4"},
		{Kind: "message", Role: "user", Text: "legacy", Timestamp: "2026-10-19T10:04:00Z"},
		{Kind: "message", Role: "assistant", Text: "four"},
	}
	exchanges := ExchangesFromItems(items, "journal")
	type want struct{ user, origin, sender, assistant string }
	wants := []want{
		{"first", "", "", "one"},
		{"after the close", "peer", "reviewer", "answered"},
		{"build finished", "task-notification", "", "two"},
		{"next task", "peer", "collab", "three"},
		{"<app context>\n\nhow does it work?", "", "", "like this"},
		{"legacy", "", "", "four"},
	}
	if len(exchanges) != len(wants) {
		t.Fatalf("exchanges=%+v", exchanges)
	}
	for index, expected := range wants {
		exchange := exchanges[index]
		if got := (want{exchange.User, exchange.Origin, exchange.Sender, exchange.Assistant}); got != expected || exchange.Timestamp.IsZero() {
			t.Fatalf("exchange %d = %+v want %+v", index, exchange, expected)
		}
	}
}

func TestExchangeStartsNeverCrossAnEndedTurn(t *testing.T) {
	reused := []TimelineItem{
		{Kind: "message", Role: "user", Text: "first", TurnID: "t1"},
		{Kind: "message", Role: "assistant", Text: "one", TurnID: "t1"},
		{Kind: "done", TurnID: "t1"},
		{Kind: "message", Role: "peer", Origin: "peer", Sender: "reviewer", Text: "after the close", TurnID: "t1"},
		{Kind: "message", Role: "assistant", Text: "two", TurnID: "t1"},
	}
	if got := ExchangeStarts(reused); !reflect.DeepEqual(got, []int{0, 3}) {
		t.Fatalf("starts=%v, want [0 3]", got)
	}
	injected := []TimelineItem{
		{Kind: "instructions", Text: "context", TurnID: "t1"},
		{Kind: "message", Role: "user", Text: "first", TurnID: "t1"},
		{Kind: "message", Role: "assistant", Text: "one", TurnID: "t1"},
		{Kind: "done", TurnID: "t1"},
		{Kind: "instructions", Text: "context again", TurnID: "t1"},
		{Kind: "message", Role: "user", Text: "second", TurnID: "t1"},
		{Kind: "message", Role: "assistant", Text: "two", TurnID: "t1"},
	}
	if got := ExchangeStarts(injected); !reflect.DeepEqual(got, []int{0, 4}) {
		t.Fatalf("starts=%v, want [0 4]", got)
	}
}
