package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeSubagentFixture struct {
	home       string
	transcript string
	dir        string
}

func newClaudeSubagentFixture(t *testing.T, transcriptID string) claudeSubagentFixture {
	t.Helper()
	home := t.TempDir()
	project := filepath.Join(home, ".claude", "projects", "encoded-cwd")
	transcript := filepath.Join(project, transcriptID+".jsonl")
	dir := filepath.Join(project, transcriptID, "subagents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"type":"user","sessionId":"`+transcriptID+`"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return claudeSubagentFixture{home: home, transcript: transcript, dir: dir}
}

func (f claudeSubagentFixture) write(t *testing.T, agentID, meta string, records ...string) string {
	t.Helper()
	path := filepath.Join(f.dir, "agent-"+agentID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(records, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if meta != "" {
		if err := os.WriteFile(filepath.Join(f.dir, "agent-"+agentID+".meta.json"), []byte(meta), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func subagentRecord(transcriptID, agentID, kind, stopReason string) string {
	record := `{"type":"` + kind + `","sessionId":"` + transcriptID + `","agentId":"` + agentID + `"`
	if kind == "assistant" {
		record += `,"message":{"stop_reason":"` + stopReason + `"}`
	}
	return record + "}"
}

func TestClaudeSubagentsAreReadFromTheSessionTranscriptDirectory(t *testing.T) {
	fixture := newClaudeSubagentFixture(t, "transcript-1")
	finished := fixture.write(t, "done1", `{"agentType":"Explore","description":"Map the code","toolUseId":"toolu_1","spawnDepth":1}`,
		subagentRecord("transcript-1", "done1", "user", ""),
		subagentRecord("transcript-1", "done1", "assistant", "end_turn"),
		`{"type":"attachment","sessionId":"transcript-1","agentId":"done1"}`)
	running := fixture.write(t, "run1", `{"agentType":"general-purpose","description":"Write tests","toolUseId":"toolu_2","spawnDepth":2}`,
		subagentRecord("transcript-1", "run1", "user", ""),
		subagentRecord("transcript-1", "run1", "assistant", "tool_use"))
	now := time.Now()
	if err := os.Chtimes(finished, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(fixture.home, ".claude", "projects", "encoded-cwd", "transcript-2", "subagents", "agent-x.jsonl")
	if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(`{"sessionId":"mismatched","agentId":"x"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	observer := newClaudeSubagentObserver()
	session := &surface.Session{ID: "session_bridge", Transcript: fixture.transcript}
	links, err := observer.observeSession(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("links = %+v", links)
	}
	run, done := links[0], links[1]
	if run.AgentID != "run1" || run.ParentSessionID != "session_bridge" || run.AgentType != "general-purpose" || run.Description != "Write tests" || run.ToolUseID != "toolu_2" || run.Depth != 2 || !run.Working || run.TranscriptPath != running {
		t.Fatalf("running subagent = %+v", run)
	}
	if done.AgentID != "done1" || done.AgentType != "Explore" || done.Depth != 1 || done.Working {
		t.Fatalf("finished subagent = %+v", done)
	}
	rollup, err := observer.rollup(context.Background(), session)
	if err != nil || rollup == nil || *rollup != (surface.SubagentRollup{Count: 2, Working: 1}) {
		t.Fatalf("rollup = %+v, %v", rollup, err)
	}
}

func TestClaudeSubagentStateFollowsAppendedRecordsAndNewFiles(t *testing.T) {
	fixture := newClaudeSubagentFixture(t, "transcript-1")
	path := fixture.write(t, "a1", "", subagentRecord("transcript-1", "a1", "user", ""))
	observer := newClaudeSubagentObserver()
	session := &surface.Session{ID: "transcript-1", Transcript: fixture.transcript}
	links, err := observer.observeSession(context.Background(), session)
	if err != nil || len(links) != 1 || !links[0].Working || links[0].Depth != 1 || links[0].AgentType != "" {
		t.Fatalf("initial = %+v, %v", links, err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(subagentRecord("transcript-1", "a1", "assistant", "end_turn") + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	fixture.write(t, "a2", `{"agentType":"Plan","spawnDepth":1}`, subagentRecord("transcript-1", "a2", "user", ""))
	if err := os.Chtimes(fixture.dir, later, later); err != nil {
		t.Fatal(err)
	}
	links, err = observer.observeSession(context.Background(), session)
	if err != nil || len(links) != 2 {
		t.Fatalf("after append = %+v, %v", links, err)
	}
	byID := map[string]surface.ClaudeSubagentLink{}
	for _, link := range links {
		byID[link.AgentID] = link
	}
	if byID["a1"].Working || !byID["a2"].Working || byID["a2"].AgentType != "Plan" {
		t.Fatalf("after append = %+v", byID)
	}

	stale := time.Now().Add(-claudeSubagentStaleAfter - time.Minute)
	if err := os.Chtimes(filepath.Join(fixture.dir, "agent-a2.jsonl"), stale, stale); err != nil {
		t.Fatal(err)
	}
	rollup, err := observer.rollup(context.Background(), session)
	if err != nil || rollup == nil || *rollup != (surface.SubagentRollup{Count: 2, Working: 0}) {
		t.Fatalf("a subagent with no terminal record stops working once stale: %+v, %v", rollup, err)
	}
}

func TestClaudeSubagentsRejectMismatchedIdentityAndIgnoreMissingDirectories(t *testing.T) {
	fixture := newClaudeSubagentFixture(t, "transcript-1")
	fixture.write(t, "a1", "", `{"sessionId":"other","agentId":"a1"}`)
	observer := newClaudeSubagentObserver()
	if _, err := observer.observeSession(context.Background(), &surface.Session{ID: "s", Transcript: fixture.transcript}); err == nil {
		t.Fatal("expected a transcript whose record names another session to be rejected")
	}
	links, err := observer.observeSession(context.Background(), &surface.Session{ID: "s", Transcript: filepath.Join(fixture.home, "none.jsonl")})
	if err != nil || len(links) != 0 {
		t.Fatalf("missing directory = %+v, %v", links, err)
	}
	rollup, err := observer.rollup(context.Background(), &surface.Session{ID: "s"})
	if err != nil || rollup != nil {
		t.Fatalf("session without a transcript = %+v, %v", rollup, err)
	}
}

func TestObserveAllClaudeSubagentsKeysParentsByTranscript(t *testing.T) {
	fixture := newClaudeSubagentFixture(t, "transcript-1")
	fixture.write(t, "a1", "", subagentRecord("transcript-1", "a1", "user", ""))
	links, err := ObserveAllClaudeSubagents(context.Background(), fixture.home)
	if err != nil || len(links) != 1 || links[0].ParentSessionID != "transcript-1" || links[0].AgentID != "a1" {
		t.Fatalf("links = %+v, %v", links, err)
	}
}
