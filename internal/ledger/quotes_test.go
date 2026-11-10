package ledger_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestQuotedTextIsToldApartFromWhatThePersonTyped(t *testing.T) {
	answered := ledger.QuoteSource{ItemID: "r2", Text: "I can add the index now. Should search also match typos like 'serach' and 'seach'?"}
	older := ledger.QuoteSource{ItemID: "r1", Text: "Plan: the export writes one CSV per project, streamed so large projects never load into memory."}
	replies := []ledger.QuoteSource{answered, older}
	cases := map[string]struct {
		text   string
		quoted []string
		ids    []string
		typed  string
		marked string
	}{
		"a quoted block of the answered reply": {
			text:   "> Should search also match typos like 'serach' and 'seach'?\nyes, both",
			quoted: []string{"> Should search also match typos like 'serach' and 'seach'?"},
			ids:    []string{"r2"},
			typed:  "yes, both",
		},
		"a pasted stretch of an older reply, wrapped differently": {
			text:   "about this, the export writes one CSV per\nproject, streamed so large projects never load -- make it a zip instead",
			quoted: []string{"the export writes one CSV per\nproject, streamed so large projects never load"},
			ids:    []string{"r1"},
			typed:  "about this,\n-- make it a zip instead",
			marked: "about this,\n> the export writes one CSV per\n> project, streamed so large projects never load\n-- make it a zip instead",
		},
		"a match shorter than the floor is typed": {
			text:  "the export writes one CSV per project, please",
			typed: "the export writes one CSV per project, please",
		},
		"a quoted block from elsewhere": {
			text:   "> from the design doc: never block capture\n>\n> ever\nagreed",
			quoted: []string{"> from the design doc: never block capture\n>\n> ever"},
			ids:    []string{""},
			typed:  "agreed",
		},
		"only a quote": {
			text:   "> Should search also match typos like 'serach' and 'seach'?",
			quoted: []string{"> Should search also match typos like 'serach' and 'seach'?"},
			ids:    []string{"r2"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			quotes := ledger.FindQuotes(c.text, replies)
			var ids []string
			for _, q := range quotes {
				ids = append(ids, q.ReplyItemID)
			}
			if got := ledger.QuotedText(c.text, quotes); !slices.Equal(got, c.quoted) || !slices.Equal(ids, c.ids) {
				t.Fatalf("quotes %q from %q, want %q from %q", got, ids, c.quoted, c.ids)
			}
			if got := ledger.Typed(c.text, quotes); got != c.typed {
				t.Fatalf("typed %q, want %q", got, c.typed)
			}
			if c.marked != "" {
				if got := ledger.Marked(c.text, quotes); got != c.marked {
					t.Fatalf("marked %q, want %q", got, c.marked)
				}
			}
		})
	}
}

func TestQuotesInMultibyteTextKeepWholeCharacters(t *testing.T) {
	reply := ledger.QuoteSource{ItemID: "r1", Text: "検索は全文検索にしますか、それとも前方一致だけにしますか。誤字にも対応したほうがよいと思います。"}
	text := "はい：検索は全文検索にしますか、それとも前方一致だけにしますか。誤字にも対応したほうがよいと思います。 全文で"
	quotes := ledger.FindQuotes(text, []ledger.QuoteSource{reply})
	if got := ledger.QuotedText(text, quotes); len(got) != 1 || got[0] != reply.Text {
		t.Fatalf("quoted %q", got)
	}
	if typed := ledger.Typed(text, quotes); typed != "はい：\n全文で" {
		t.Fatalf("typed %q", typed)
	}
	if typed := ledger.Typed(text, []ledger.Quote{{Start: 1, End: 4}}); typed != text {
		t.Fatalf("a quote that splits a character was applied: %q", typed)
	}
}

func TestAStretchPastedAcrossTwoRepliesIsNeverOneQuote(t *testing.T) {
	first := ledger.QuoteSource{ItemID: "r2", Text: "Step one is to migrate the orders table and then backfill the totals column"}
	second := ledger.QuoteSource{ItemID: "r1", Text: "migrate the orders table and then backfill the totals column before the deploy window opens"}
	text := "Step one is to migrate the orders table and then backfill the totals column before the deploy window opens, fine"
	quotes := ledger.FindQuotes(text, []ledger.QuoteSource{first, second})
	if len(quotes) == 0 {
		t.Fatal("no quotes found")
	}
	byID := map[string]string{"r1": second.Text, "r2": first.Text}
	for i, quoted := range ledger.QuotedText(text, quotes) {
		if !strings.Contains(byID[quotes[i].ReplyItemID], quoted) {
			t.Fatalf("quote %q is not part of %s, the reply it names", quoted, quotes[i].ReplyItemID)
		}
	}
}

func TestAQuotedBlockWithABlankQuotedLineKeepsItsReply(t *testing.T) {
	reply := ledger.QuoteSource{ItemID: "r1", Text: "Shall I ship it?\n\nThe tests pass."}
	for _, newline := range []string{"\n", "\r\n"} {
		text := strings.Join([]string{"> Shall I ship it?", ">", "> The tests pass.", "yes"}, newline)
		if quotes := ledger.FindQuotes(text, []ledger.QuoteSource{reply}); len(quotes) != 1 || quotes[0].ReplyItemID != "r1" {
			t.Fatalf("quotes %+v in %q", quotes, text)
		}
	}
}

func TestAQuoteIsTheLongestStretchAnyReplyHolds(t *testing.T) {
	newer := ledger.QuoteSource{ItemID: "r2", Text: "The migration renames the orders table to purchases, then stops for a review."}
	older := ledger.QuoteSource{ItemID: "r1", Text: "The migration renames the orders table to purchases, then copies every row into the archive in batches of a thousand."}
	text := "re: The migration renames the orders table to purchases, then copies every row into the archive in batches of a thousand. too slow"
	quotes := ledger.FindQuotes(text, []ledger.QuoteSource{newer, older})
	if got := ledger.QuotedText(text, quotes); len(got) != 1 || got[0] != older.Text || quotes[0].ReplyItemID != "r1" {
		t.Fatalf("quotes %q from %+v, want the whole older reply", got, quotes)
	}
}

func TestAQuoteDeepInALongMessageIsFound(t *testing.T) {
	reply := ledger.QuoteSource{ItemID: "r1", Text: "Rotate the signing key every ninety days and keep the previous one for verification."}
	text := strings.Repeat("typed notes ", 300<<10/12) + "\n" + reply.Text + "\nok"
	quotes := ledger.FindQuotes(text, []ledger.QuoteSource{reply})
	if got := ledger.QuotedText(text, quotes); len(got) != 1 || got[0] != reply.Text {
		t.Fatalf("quotes %q", got)
	}
}
