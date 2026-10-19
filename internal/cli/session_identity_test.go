package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/signin"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const (
	originalConversation = "0f3c1d52-7f0e-4c1a-9a43-1d2e8b7c6a01"
	branchConversation   = "6b9e2a17-2c44-4d8e-b0f5-3e7a9c1d4b02"
	liveBridge           = "session_live"
)

func liveClaudeApp(t *testing.T) (*App, *registry.Registry, *claudetest.Fixture, *claudePosts) {
	t.Helper()
	fixture := claudetest.New(t)
	posts := &claudePosts{}
	app, r := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	app.Surfaces[0].Surface = surfaces.NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, posts.request)
	return app, r, fixture, posts
}

func branchTranscript(t *testing.T, fixture *claudetest.Fixture, conversation, parent string) {
	t.Helper()
	first, _ := json.Marshal(map[string]any{"type": "user", "uuid": "fixture-branch", "sessionId": conversation, "cwd": fixture.Home, "forkedFrom": map[string]any{"sessionId": parent, "messageUuid": "fixture-assistant"}, "message": map[string]any{"role": "user", "content": "fixture branch question"}})
	answer, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": "fixture-branch-answer", "sessionId": conversation, "cwd": fixture.Home, "message": map[string]any{"id": "fixture-branch-message", "stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": "fixture branch answer"}}}})
	duration, _ := json.Marshal(map[string]any{"type": "system", "subtype": "turn_duration", "sessionId": conversation, "cwd": fixture.Home})
	path := filepath.Join(fixture.Home, ".claude", "projects", "fixture-project", conversation+".jsonl")
	if err := os.WriteFile(path, []byte(string(first)+"\n"+string(answer)+"\n"+string(duration)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func whoami(t *testing.T, app *App, conversation string) whoamiResult {
	t.Helper()
	t.Setenv("CLAUDE_CODE_SESSION_ID", conversation)
	output, err := captureStdout(t, func() error { return app.Run([]string{"whoami", "--json"}) })
	var result whoamiResult
	if err != nil || json.Unmarshal([]byte(output), &result) != nil {
		t.Fatalf("whoami output=%q err=%v", output, err)
	}
	return result
}

func sendFrom(t *testing.T, app *App, r *registry.Registry, caller, target, message string) string {
	t.Helper()
	t.Setenv("CLAUDE_CODE_SESSION_ID", caller)
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "--json", target, message}) })
	var receipt struct {
		DeliveryID int64 `json:"deliveryId"`
	}
	if err != nil || json.Unmarshal([]byte(output), &receipt) != nil || receipt.DeliveryID == 0 {
		t.Fatalf("send %s output=%q err=%v", target, output, err)
	}
	intent, err := r.DeliveryIntent(receipt.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	return intent.SenderSessionID
}

func TestBranchInALiveProcessKeepsTheAliasOnTheOriginalConversation(t *testing.T) {
	app, r, fixture, posts := liveClaudeApp(t)
	fixture.Transcript(originalConversation, fixture.Home)
	fixture.Record(originalConversation, fixture.Home, liveBridge)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"identify", "claude:" + originalConversation, "worker"}) }); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(originalConversation, "held for the original"); err != nil {
		t.Fatal(err)
	}

	branchTranscript(t, fixture, branchConversation, originalConversation)
	fixture.Record(branchConversation, fixture.Home, liveBridge)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"list", "--all"}) }); err != nil {
		t.Fatal(err)
	}

	if owner, err := r.LookupAlias("worker"); err != nil || owner != originalConversation {
		t.Fatalf("alias owner=%q err=%v", owner, err)
	}
	if r.QueueCount(originalConversation) != 1 || r.QueueCount(branchConversation) != 0 {
		t.Fatalf("queues original=%d branch=%d", r.QueueCount(originalConversation), r.QueueCount(branchConversation))
	}
	branch, err := r.Session(branchConversation)
	if err != nil || branch.ForkedFrom != originalConversation || branch.PID != fixture.PID || branch.Name == "worker" {
		t.Fatalf("branch=%+v err=%v", branch, err)
	}
	if original, err := r.Session(originalConversation); err != nil || original.PID != 0 {
		t.Fatalf("the original keeps the branched process: %+v err=%v", original, err)
	}
	if got := whoami(t, app, branchConversation); !got.Resolved || got.Session != branchConversation || got.Alias != "" {
		t.Fatalf("branch whoami=%+v", got)
	}
	if got := whoami(t, app, originalConversation); !got.Resolved || got.Session != originalConversation || got.Alias != "worker" {
		t.Fatalf("original whoami=%+v", got)
	}

	if sender := sendFrom(t, app, r, originalConversation, "claude:"+branchConversation, "to the branch"); sender != originalConversation || posts.count("to the branch") != 1 {
		t.Fatalf("send to the branch: sender=%q posts=%d", sender, posts.count("to the branch"))
	}
	fixture.ResumeBridge("session_resumed")
	if sender := sendFrom(t, app, r, branchConversation, "@worker", "to the original"); sender != branchConversation || posts.count("to the original") != 1 {
		t.Fatalf("send to @worker: sender=%q posts=%d", sender, posts.count("to the original"))
	}
	if fixture.Invocations("--bg", "--resume", originalConversation) != 1 {
		t.Fatal("a send to the original was not delivered by resuming the original conversation")
	}
}

func TestResumeInAnotherProcessKeepsTheAlias(t *testing.T) {
	app, r, fixture, _ := liveClaudeApp(t)
	fixture.Transcript(originalConversation, fixture.Home)
	fixture.Record(originalConversation, fixture.Home, liveBridge)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"identify", "claude:" + originalConversation, "worker"}) }); err != nil {
		t.Fatal(err)
	}

	fixture.RemoveRecord()
	fixture.Record(originalConversation, fixture.Home, "session_other")
	if _, err := captureStdout(t, func() error { return app.Run([]string{"list", "--all"}) }); err != nil {
		t.Fatal(err)
	}

	if owner, err := r.LookupAlias("worker"); err != nil || owner != originalConversation {
		t.Fatalf("alias owner=%q err=%v", owner, err)
	}
	sessions, err := r.ListSessions(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != originalConversation {
		t.Fatalf("a resume created another row: %+v", sessions)
	}
	if got := whoami(t, app, originalConversation); !got.Resolved || got.Alias != "worker" {
		t.Fatalf("whoami=%+v", got)
	}
}

func TestALaunchedSessionAndItsDiscoveredProcessAreOneRow(t *testing.T) {
	app, r, fixture, _ := liveClaudeApp(t)
	launched := surface.Session{ID: originalConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "launched", PID: fixture.PID, Status: surface.StatusBusy, Cwd: fixture.Home}
	if err := r.RegisterSession(launched); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"identify", "claude:" + originalConversation, "relay"}) }); err != nil {
		t.Fatal(err)
	}

	transcript := fixture.Transcript(originalConversation, fixture.Home)
	fixture.Record(originalConversation, fixture.Home, liveBridge)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"list", "--all"}) }); err != nil {
		t.Fatal(err)
	}

	sessions, err := r.ListSessions(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != originalConversation || sessions[0].Transcript != transcript || sessions[0].Bridge != liveBridge || sessions[0].PID != fixture.PID {
		t.Fatalf("sessions=%+v", sessions)
	}
	if owner, err := r.LookupAlias("relay"); err != nil || owner != originalConversation {
		t.Fatalf("alias owner=%q err=%v", owner, err)
	}
}

func TestVersionReportsTheRegistrySchema(t *testing.T) {
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	output, err := captureStdout(t, func() error { return app.Run([]string{"version", "--json"}) })
	var version struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err != nil || json.Unmarshal([]byte(output), &version) != nil || version.SchemaVersion != registry.SchemaVersion() || version.SchemaVersion == 0 {
		t.Fatalf("output=%q err=%v", output, err)
	}
}
