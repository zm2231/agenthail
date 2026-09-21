package surface

import "testing"

func TestSessionReadFromTimelineBuildsBoundedExchangesAndReply(t *testing.T) {
	session := &Session{Status: StatusIdle}
	timeline := &SessionTimeline{
		Source:     "local-transcript",
		NextBefore: 44,
		Items: []TimelineItem{
			{Kind: "message", Role: "user", Text: "first"},
			{Kind: "message", Role: "assistant", Text: "one"},
			{Kind: "toolCall", Title: "exec", Text: "ignored"},
			{Kind: "message", Role: "user", Text: "second"},
			{Kind: "message", Role: "assistant", Text: "two"},
		},
	}

	result := SessionReadFromTimeline(session, timeline, 1)
	if result.Source != "local-transcript" || result.NextBefore != 44 || len(result.Items) != 5 || len(result.Exchanges) != 1 {
		t.Fatalf("result=%+v", result)
	}
	if result.Exchanges[0].User != "second" || result.Exchanges[0].Assistant != "two" || result.Exchanges[0].Source != "local-transcript" {
		t.Fatalf("exchange=%+v", result.Exchanges[0])
	}
	if result.Reply == nil || result.Reply.Text != "two" || !result.Reply.Done || result.Reply.Source != "local-transcript" {
		t.Fatalf("reply=%+v", result.Reply)
	}
}

func TestSessionReadMarksLatestReplyIncompleteWhileBusy(t *testing.T) {
	result := SessionReadFromTimeline(&Session{Status: StatusBusy}, &SessionTimeline{
		Source: "claude",
		Items:  []TimelineItem{{Kind: "message", Role: "assistant", Text: "checking"}},
	}, 1)
	if result.Reply == nil || result.Reply.Done {
		t.Fatalf("reply=%+v", result.Reply)
	}
}
