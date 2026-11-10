package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestLedgerWordsPrintsWhatWasTypedQuotedAndTheTurnsAround(t *testing.T) {
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	const key = "repo:github.com/acme/widget"
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: key}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err := store.RegisterSession(surface.Session{ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	entry := func(item, role, body string, at time.Time) {
		data, _ := json.Marshal(map[string]any{"itemId": item, "version": 1, "op": "upsert", "kind": "message", "role": role, "body": body, "ts": at.Format(time.RFC3339Nano)})
		if _, _, err := store.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: "s1", Kind: "message", ProviderKey: item, Payload: data, ObservedAt: now}, registry.SessionJournalRetention{Count: 64, Bytes: 1 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	text := "> Should search match typos?\nyes, both kinds"
	entry("r1", "assistant", "Should search match typos?", now.Add(-time.Minute))
	entry("u1", "user", text, now)
	entry("r2", "assistant", "Added typo matching.", now.Add(time.Second))
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	if _, _, err := ls.RecordUtterance(ledger.Utterance{ProjectKey: key, Ref: "claude-prompt:1", SpeakerID: "person", TargetID: "s1", Text: text, At: now, CapturedBy: ledger.CapturedByHook}); err != nil {
		t.Fatal(err)
	}
	reply := ledger.NewReply("r1", now.Add(-time.Minute).Format(time.RFC3339Nano), "Should search match typos?")
	quotes := ledger.FindQuotes(text, []ledger.QuoteSource{{ItemID: "r1", Text: reply.Text}})
	if _, err := ls.RecordContext(key, ledger.ContextPayload{UtteranceID: "claude-prompt:1", Status: ledger.ReplyFound, JoinedBy: ledger.JoinedByID, EntryItemID: "u1", Reply: reply, Quotes: quotes}, now); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"words", "claude-prompt:1", "--context", "1"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  typed: yes, both kinds\n  quoting: > Should search match typos?\n  answering: Should search match typos?\n",
		"turns around it (found by its own entry):\n",
		" agent: Should search match typos?\n",
		" person, this message: > Should search match typos?\n    yes, both kinds\n",
		" agent: Added typo matching.\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("words lack %q:\n%s", want, out)
		}
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"words", "claude-prompt:1", "--context", "21"}) }); err == nil || !strings.Contains(err.Error(), "0 to 20") {
		t.Fatalf("too many turns = %v", err)
	}
}
