package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestACodexJournalFromAnOlderMappingIsReadAgainFromItsTranscript(t *testing.T) {
	_, reg, _, from, _ := daemonFixture(t)
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}
{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"call-1","input":"text((await tools.exec_command({cmd:\"make test\"})).output)"}}
{"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"CommandExecution","id":"exec-1","command":["/bin/zsh","-lc","make test"],"aggregated_output":"ok\n","exit_code":0,"status":"completed"}}}
{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call-1","output":[{"type":"input_text","text":"Script completed\nWall time 0.2 seconds\nOutput:\nok\n"}]}}
`), 0600); err != nil {
		t.Fatal(err)
	}
	from.Source, from.Transport, from.Transcript, from.HasLocal = "vscode", "desktop", transcript, true
	if err := reg.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	adapter := surfaces.NewCodex("http://127.0.0.1:1")
	manager := newTestSourceManager(t, reg)
	first, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if version, err := reg.SessionJournalDerivation(from.ID); err != nil || version != codexJournalDerivation {
		t.Fatalf("fresh seed derivation=%d err=%v", version, err)
	}
	first.Cancel()
	waitForSessionSourceGone(t, manager)
	stale, _ := json.Marshal(sessionJournalPayload{ItemID: "codex:transcript:old", ProviderKey: "codex:transcript:old", Op: "upsert", Kind: "event", Title: "Codex activity", Body: `item_completed: {"item":{}}`, TS: time.Now().UTC().Format(time.RFC3339Nano)})
	if _, _, err := reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: from.ID, Kind: "event", ProviderKey: "codex:transcript:old", Payload: stale, ObservedAt: time.Now().UTC()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}); err != nil {
		t.Fatal(err)
	}
	if err := reg.MarkSessionJournalDerivation(from.ID, 0); err != nil {
		t.Fatal(err)
	}
	second, err := manager.prepareStream(context.Background(), &from, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cancel()
	page, err := reg.ReadSessionJournalPage(from.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	var calls, results int
	for _, entry := range page.Entries {
		var payload sessionJournalPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Op == "remove" {
			continue
		}
		switch {
		case payload.Title == "Codex activity":
			t.Fatalf("stale row survived: %+v", payload)
		case payload.Kind == "toolCall" && payload.Title == "exec_command" && payload.Body == `{"cmd":"make test"}`:
			calls++
		case payload.Kind == "toolResult" && payload.Body == "ok\n":
			results++
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("calls=%d results=%d page=%+v", calls, results, page.Entries)
	}
	if version, err := reg.SessionJournalDerivation(from.ID); err != nil || version != codexJournalDerivation {
		t.Fatalf("derivation=%d err=%v", version, err)
	}
}
