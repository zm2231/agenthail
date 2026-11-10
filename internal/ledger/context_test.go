package ledger_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/ledger"
)

func answered(t *testing.T, s *ledger.Store, ref, status, joinedBy, reply string) {
	t.Helper()
	p := ledger.ContextPayload{UtteranceID: ref, Status: status, JoinedBy: joinedBy}
	if reply != "" {
		p.Reply = ledger.NewReply("r-"+ref, time.Now().UTC().Format(time.RFC3339Nano), reply)
	}
	if _, err := s.RecordContext(project, p, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestTheWordsALeadReadsCarryTheReplyTheyAnswered(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	answered(t, s, "claude-prompt:1", ledger.ReplyFound, ledger.JoinedByID, "Should search match typos?")
	items(t, s, "add the index")
	capture(t, s, project, "claude-prompt:2", "yes")
	long := strings.Repeat("détail ", 400) + "Ship it?"
	answered(t, s, "claude-prompt:2", ledger.ReplyFound, ledger.JoinedByID, long)
	capture(t, s, project, "claude-prompt:3", "and the export")
	answered(t, s, "claude-prompt:3", ledger.ReplyLate, "", "")

	ctx, ok := board(t, s).WorkContext("widget-W1")
	if !ok || ctx.Clause == nil || len(ctx.Clause.Words) != 1 || ctx.Clause.Words[0].Context == nil || ctx.Clause.Words[0].Context.Reply.Text != "Should search match typos?" {
		t.Fatalf("the item's words lack the reply they answered: %+v", ctx.Clause)
	}

	read, err := s.Read(project, ledger.ReadRequest{View: "board"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(read)
	var view struct {
		Provisional struct {
			Newest []ledger.UtteranceView `json:"newest"`
		} `json:"provisional"`
	}
	if err := json.Unmarshal(data, &view); err != nil || len(view.Provisional.Newest) != 2 {
		t.Fatalf("board provisional = %s (%v)", data, err)
	}
	yes, late := view.Provisional.Newest[0], view.Provisional.Newest[1]
	if c := yes.Context; c == nil || !strings.HasSuffix(c.Reply.Text, "Ship it?") || len(c.Reply.Text) > 600 || !utf8.ValidString(c.Reply.Text) || c.Reply.Runes != utf8.RuneCountInString(long) {
		t.Fatalf("board reply for %s = %+v, want the end of a long reply with its full length", yes.ID, c)
	}
	if c := late.Context; c == nil || c.Status != ledger.ReplyLate || c.Reply != nil {
		t.Fatalf("board context for %s = %+v, want late with no reply", late.ID, c)
	}

	words := board(t, s).Utterances()
	if full := words[1].Context.Reply.Text; len([]rune(full)) != ledger.ContextReplyRunes {
		t.Fatalf("the board view clipped the stored reply to %d runes", len([]rune(full)))
	}
}
