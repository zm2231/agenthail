package surface

import "testing"

func TestBoundSessionReadTrimsToNewestExchangesAndKeepsReaderReply(t *testing.T) {
	session := &Session{Status: StatusIdle}
	result := BoundSessionRead(session, &SessionReadResult{
		Source:    "local-transcript",
		Exchanges: []Exchange{{User: "first", Assistant: "one"}, {User: "second", Assistant: "two"}},
	}, 1)
	if len(result.Exchanges) != 1 || result.Exchanges[0].User != "second" || result.Exchanges[0].Source != "local-transcript" || len(result.Items) != 0 {
		t.Fatalf("result=%+v", result)
	}
	if result.Reply == nil || result.Reply.Text != "two" || !result.Reply.Done || result.Reply.Source != "local-transcript" {
		t.Fatalf("reply=%+v", result.Reply)
	}
	busy := BoundSessionRead(&Session{Status: StatusBusy}, &SessionReadResult{Source: "rpc", Exchanges: []Exchange{{Assistant: "checking"}}}, 1)
	if busy.Reply == nil || busy.Reply.Done {
		t.Fatalf("reply=%+v", busy.Reply)
	}
	preset := &ReplyResult{Text: "partial", Done: false, Source: "local-transcript"}
	kept := BoundSessionRead(&Session{Status: StatusIdle}, &SessionReadResult{Source: "local-transcript", Exchanges: []Exchange{{Assistant: "partial"}}, Reply: preset}, 1)
	if kept.Reply != preset || kept.Reply.Done {
		t.Fatalf("reader reply was replaced: %+v", kept.Reply)
	}
}
