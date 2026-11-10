package daemon

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/ledger"
)

func capturedWithContext(t *testing.T, handler func(), p ledgerProject, ref string) ledger.ContextPayload {
	t.Helper()
	handler()
	eventually(t, "the message's context", func() bool { _, ok := recordedContext(t, p, ref); return ok })
	c, _ := recordedContext(t, p, ref)
	return c
}

func TestAMessagesWordsShowWhatWasTypedQuotedAndTheTurnsAroundIt(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, search := sorterFixture(t, stub)
	stub.setAnswer(fileTo(search))
	d.contexts.wait = 10 * time.Second
	s := p.session(t, r, "claude-words")
	journal(t, r, s.ID, "r0", map[string]any{"kind": "message", "role": "assistant", "body": "Started on the index.", "ts": at(-2 * time.Minute)})
	journal(t, r, s.ID, "r1", map[string]any{"kind": "message", "role": "assistant", "body": "Should search match typos?", "ts": at(-time.Minute)})
	message := "> Should search match typos?\nyes, both kinds"
	journal(t, r, s.ID, "u1", map[string]any{"kind": "message", "role": "user", "promptId": "p-1", "body": message, "ts": at(0)})
	journal(t, r, s.ID, "t1", map[string]any{"kind": "tool", "role": "tool", "body": "ran tests", "ts": at(time.Second)})
	journal(t, r, s.ID, "r2", map[string]any{"kind": "message", "role": "assistant", "body": "Added typo matching.", "ts": at(2 * time.Second)})
	runSorter(t, d)
	c := capturedWithContext(t, func() {
		ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-1", Prompt: message})
	}, p, ledger.PromptRef("p-1"))
	if c.EntryItemID != "u1" {
		t.Fatalf("context = %+v, want it joined to entry u1", c)
	}

	words, err := ReadWords(r, p.store, p.key, ledger.PromptRef("p-1"), 1)
	if err != nil {
		t.Fatal(err)
	}
	var turns []string
	for _, turn := range words.Turns {
		turns = append(turns, turn.ItemID+map[bool]string{true: "*"}[turn.Message])
	}
	if words.Typed != "yes, both kinds" || len(words.Quoted) != 1 || words.Anchor != "entry" || strings.Join(turns, ",") != "r1,u1*,r2" {
		t.Fatalf("words typed %q quoted %q anchor %s turns %v", words.Typed, words.Quoted, words.Anchor, turns)
	}

	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, s))
	read, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "words", "id": ledger.PromptRef("p-1"), "context": 2})
	if list, _ := read["turns"].([]any); refused || read["typed"] != "yes, both kinds" || len(list) != 4 {
		t.Fatalf("mcp words = %v", read)
	}
	if refusal, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "words", "id": ledger.PromptRef("p-1"), "context": 6}); !refused || refusal["code"] != "invalid" {
		t.Fatalf("mcp words with too much context = %v", refusal)
	}
	if refusal, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "words", "id": ledger.PromptRef("p-9")}); !refused || refusal["code"] != "not_found" {
		t.Fatalf("mcp words for an unknown message = %v", refusal)
	}
}

func TestTheTurnsAroundWordsAreReadOnlyOnTheHostThatCapturedThem(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, search := sorterFixture(t, stub)
	stub.setAnswer(fileTo(search))
	s := p.session(t, r, "claude-elsewhere")
	journal(t, r, s.ID, "u1", map[string]any{"kind": "message", "role": "user", "promptId": "p-1", "body": "ship it", "ts": at(0)})
	runSorter(t, d)
	capturedWithContext(t, func() {
		ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-1", Prompt: "ship it"})
	}, p, ledger.PromptRef("p-1"))
	if _, err := r.WriteDB().Exec(`UPDATE ledger_events SET actor_host_id='hfar' WHERE project_key=? AND ref=?`, p.key, ledger.PromptRef("p-1")); err != nil {
		t.Fatal(err)
	}

	words, err := ReadWords(r, p.store, p.key, ledger.PromptRef("p-1"), 0)
	if err != nil || words.Host != "hfar" || words.Typed != "ship it" {
		t.Fatalf("words = %+v, %v", words, err)
	}
	var elsewhere WordsElsewhere
	if _, err := ReadWords(r, p.store, p.key, ledger.PromptRef("p-1"), 1); !errors.As(err, &elsewhere) || elsewhere.Host != "hfar" {
		t.Fatalf("turns from another host's message = %v", err)
	}
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, s))
	if refusal, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "words", "id": ledger.PromptRef("p-1"), "context": 1}); !refused || refusal["code"] != "unavailable" || !strings.Contains(refusal["message"].(string), "hfar") {
		t.Fatalf("mcp words from another host = %v", refusal)
	}

	if _, err := r.WriteDB().Exec(`UPDATE ledger_events SET actor_host_id='', origin_seq=0 WHERE project_key=? AND ref=?`, p.key, ledger.PromptRef("p-1")); err != nil {
		t.Fatal(err)
	}
	self, err := r.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	words, err = ReadWords(r, p.store, p.key, ledger.PromptRef("p-1"), 1)
	if err != nil || words.Host != self.Name || len(words.Turns) != 1 || !words.Turns[0].Message {
		t.Fatalf("words written before hosts had ids = %+v, %v", words, err)
	}
}

func TestLongWordsFullOfQuotesStillFitOneMCPRead(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, search := sorterFixture(t, stub)
	stub.setAnswer(fileTo(search))
	s := p.session(t, r, "claude-long-words")
	var blocks []string
	for i := range 17 {
		blocks = append(blocks, "> "+strings.Repeat("é\"\x01", 200), fmt.Sprintf("point %d", i))
	}
	message := strings.Join(blocks, "\n")
	promptID, itemID := strings.Repeat("p", 20<<10), strings.Repeat("u", 20<<10)
	journal(t, r, s.ID, itemID, map[string]any{"kind": "message", "role": "user", "promptId": promptID, "body": message, "ts": at(0)})
	runSorter(t, d)
	c := capturedWithContext(t, func() {
		ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: promptID, Prompt: message})
	}, p, ledger.PromptRef(promptID))
	if len(c.Quotes) != 17 || c.EntryItemID != itemID {
		t.Fatalf("context quotes = %d, entry %d bytes", len(c.Quotes), len(c.EntryItemID))
	}

	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, s))
	read, refused := callMCP(t, session, "agenthail_ledger_read", map[string]any{"view": "words", "id": ledger.PromptRef(promptID), "context": 1})
	quoted, _ := read["quoted"].([]any)
	if refused || len(quoted) == 0 || read["moreQuoted"] == nil || !strings.Contains(read["typed"].(string), "point 0") {
		t.Fatalf("mcp words = %v", read)
	}
	for _, q := range append(quoted, read["text"], read["typed"]) {
		if !utf8.ValidString(q.(string)) || strings.ContainsRune(q.(string), utf8.RuneError) {
			t.Fatalf("clipped words are not whole characters: %q", q)
		}
	}
}

func TestWordsTheSessionNeverShowedArePlacedAmongItsTurnsByTime(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, search := sorterFixture(t, stub)
	stub.setAnswer(fileTo(search))
	s := p.session(t, r, "claude-late-words")
	journal(t, r, s.ID, "r1", map[string]any{"kind": "message", "role": "assistant", "body": "Done.", "ts": at(-time.Minute)})
	journal(t, r, s.ID, "r2", map[string]any{"kind": "message", "role": "assistant", "body": "Also tidied the tests.", "ts": at(time.Hour)})
	runSorter(t, d)
	c := capturedWithContext(t, func() {
		ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-1", Prompt: "thanks"})
	}, p, ledger.PromptRef("p-1"))
	if c.Status != ledger.ReplyLate {
		t.Fatalf("context = %+v", c)
	}
	words, err := ReadWords(r, p.store, p.key, ledger.PromptRef("p-1"), 1)
	if err != nil || words.Anchor != "time" || len(words.Turns) != 2 || words.Turns[0].ItemID != "r1" || words.Turns[1].ItemID != "r2" || words.Turns[0].Message || words.Turns[1].Message {
		t.Fatalf("words = %+v, %v", words, err)
	}
}
