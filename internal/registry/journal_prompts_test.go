package registry

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func appendPrompt(t *testing.T, r *Registry, sessionID, key, role, origin, body, ts string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"itemId": key, "providerKey": key, "kind": "message", "role": role, "origin": origin, "body": body, "ts": ts})
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: sessionID, Kind: "message", ProviderKey: key, Payload: payload, ObservedAt: time.Unix(1, 0)}, SessionJournalRetention{Count: 100, Bytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
}

func promptBodies(t *testing.T, prompts []JournalPrompt) []string {
	t.Helper()
	var bodies []string
	for _, prompt := range prompts {
		bodies = append(bodies, prompt.Body)
	}
	return bodies
}

func TestJournalPromptsListsPersonMessagesFromSessionsAPersonStarted(t *testing.T) {
	r := openTestRegistry(t)
	for _, session := range []surface.Session{
		{ID: "typed", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop},
		{ID: "other", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI},
		{ID: "child", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Subagent: &surface.Subagent{ParentID: "typed", RootID: "typed", Depth: 1}},
		{ID: "scripted", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Source: "exec"},
		{ID: "started-by-agent", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI},
		{ID: "mirrored", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI},
	} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RecordSessionOrigin(OriginOf("started-by-agent", "typed")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE sessions SET host_id='hpeer' WHERE id='mirrored'`); err != nil {
		t.Fatal(err)
	}
	appendPrompt(t, r, "typed", "a", "user", "", "first prompt", "2026-10-20T10:00:00Z")
	appendPrompt(t, r, "typed", "b", "assistant", "", "a reply", "2026-10-20T10:00:01Z")
	appendPrompt(t, r, "typed", "c", "peer", "peer", "from an agent", "2026-10-20T10:00:02Z")
	appendPrompt(t, r, "other", "d", "user", "human", "second prompt", "2026-10-20T11:00:00Z")
	appendPrompt(t, r, "typed", "e", "user", "", "third prompt", "2026-10-20T12:00:00.5Z")
	appendPrompt(t, r, "child", "f", "user", "", "parent agent's prompt", "2026-10-20T13:00:00Z")
	appendPrompt(t, r, "scripted", "g", "user", "", "codex exec prompt", "2026-10-20T13:00:00Z")
	appendPrompt(t, r, "started-by-agent", "h", "user", "", "agent's prompt", "2026-10-20T13:00:00Z")
	appendPrompt(t, r, "mirrored", "i", "user", "", "a peer host's prompt", "2026-10-20T13:00:00Z")
	appendPrompt(t, r, "other", "j", "user", "task-notification", "<task-notification>", "2026-10-20T13:00:00Z")

	all, err := r.JournalPrompts(JournalPromptQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(promptBodies(t, all), "|"); got != "third prompt|second prompt|first prompt" {
		t.Fatalf("prompts = %q", got)
	}
	one, err := r.JournalPrompts(JournalPromptQuery{SessionID: "typed", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(promptBodies(t, one), "|"); got != "third prompt|first prompt" {
		t.Fatalf("session prompts = %q", got)
	}
	matched, err := r.JournalPrompts(JournalPromptQuery{Text: "SECOND", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(promptBodies(t, matched), "|"); got != "second prompt" {
		t.Fatalf("matched = %q", got)
	}
}

func TestJournalPromptsPagesByCursorAndMatchesWildcardsLiterally(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "a", "b")
	for index := range 5 {
		appendPrompt(t, r, []string{"a", "b"}[index%2], fmt.Sprintf("k%d", index), "user", "", fmt.Sprintf("prompt %d", index), "2026-10-20T10:00:00Z")
	}
	appendPrompt(t, r, "a", "pct", "user", "", "100% done", "2026-10-19T10:00:00Z")
	appendPrompt(t, r, "a", "other", "user", "", "100 done", "2026-10-19T10:00:00Z")
	var seen []string
	var cursor JournalPromptCursor
	for {
		page, err := r.JournalPrompts(JournalPromptQuery{Text: "prompt", Before: cursor, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, promptBodies(t, page)...)
		if len(page) < 2 {
			break
		}
		parsed, err := ParseJournalPromptCursor(page[len(page)-1].Cursor.String())
		if err != nil || parsed != page[len(page)-1].Cursor {
			t.Fatalf("cursor round trip: %+v %v", parsed, err)
		}
		cursor = parsed
	}
	if len(seen) != 5 || len(map[string]bool{seen[0]: true, seen[1]: true, seen[2]: true, seen[3]: true, seen[4]: true}) != 5 {
		t.Fatalf("pages = %q", seen)
	}
	literal, err := r.JournalPrompts(JournalPromptQuery{Text: "0%", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(promptBodies(t, literal), "|"); got != "100% done" {
		t.Fatalf("literal match = %q", got)
	}
	for _, raw := range []string{"x", "1:2", "nan:x:s", "1:-1:s", "1:2:"} {
		if _, err := ParseJournalPromptCursor(raw); err == nil {
			t.Fatalf("cursor %q parsed", raw)
		}
	}
}

func TestJournalPromptsReadsThePromptIndex(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "a")
	appendPrompt(t, r, "a", "k", "user", "", "prompt", "2026-10-20T10:00:00Z")
	if _, err := r.JournalPrompts(JournalPromptQuery{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ session, index string }{{"", "COVERING INDEX session_journal_prompts_at"}, {"a", "COVERING INDEX session_journal_prompts "}} {
		rows, err := r.read.Query(`EXPLAIN QUERY PLAN `+journalPromptsSQL(test.session != ""), test.session, "", "", 0.0, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, "\n")
		if !strings.Contains(joined, test.index) || strings.Contains(joined, "TEMP B-TREE") {
			t.Fatalf("session %q: plan does not read %s in order:\n%s", test.session, test.index, joined)
		}
	}
}
