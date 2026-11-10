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

func TestAnItemReadFitsWhenItsWordsAnsweredLongReplies(t *testing.T) {
	s := newStore(t)
	searchAsk(t, s)
	long := strings.Repeat("検索の詳細 ", 400) + "どうしますか?"
	answered(t, s, "claude-prompt:1", ledger.ReplyFound, ledger.JoinedByID, long)
	capture(t, s, project, "claude-prompt:2", "full-text search, I mean")
	answered(t, s, "claude-prompt:2", ledger.ReplyFound, ledger.JoinedByID, long)
	attach(t, s, ledger.AttachRequest{UtteranceID: "claude-prompt:2", As: ledger.AsRefines, To: "widget-A1", Clauses: []string{"build full-text search"}})
	items(t, s, "add the index")

	read, err := s.Read(project, ledger.ReadRequest{View: "item", ID: "widget-W1"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(read)
	var view struct {
		Item struct {
			Clause struct {
				Words []ledger.WordsRef `json:"words"`
			} `json:"clause"`
		} `json:"item"`
	}
	if err := json.Unmarshal(data, &view); err != nil || len(view.Item.Clause.Words) != 2 || len(data) > 8<<10 {
		t.Fatalf("item read is %d bytes with %d words (%v)", len(data), len(view.Item.Clause.Words), err)
	}
	for _, w := range view.Item.Clause.Words {
		if r := w.Context.Reply; !strings.HasSuffix(r.Text, "どうしますか?") || !utf8.ValidString(r.Text) || r.Runes != utf8.RuneCountInString(long) {
			t.Fatalf("%s reply = %d bytes, %d runes", w.ID, len(r.Text), r.Runes)
		}
	}

	words := board(t, s).Utterances()
	words[0].Context.Reply.Text = "changed by a reader"
	if got := board(t, s).Utterances()[0].Context.Reply.Text; got == "changed by a reader" {
		t.Fatal("a reader's copy of the words shares the board's stored reply")
	}
}
