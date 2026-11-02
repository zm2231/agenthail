package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestTerminalPromptCapturesUtteranceWhenLedgerEnabled(t *testing.T) {
	withoutSessionContextLimit(t)
	fake := &cliSurface{kind: surface.KindClaude}
	_, store := cliFixture(t, fake)

	projectDir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{
		ID: "proj-1", WorkspaceRoot: projectDir, Title: "Test", Key: "test-key",
	}); err != nil {
		t.Fatal(err)
	}
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	if err := ls.Enable("test-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterSession(surface.Session{
		ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	hooks := rootHooks(t, store, fake, 1)
	start := `{"session_id":"s1","hook_event_name":"SessionStart","source":"startup"}`
	hooks(start)

	prompt := `{"session_id":"s1","hook_event_name":"UserPromptSubmit","prompt":"build the widget"}`
	printed := hooks(prompt)

	found := false
	for _, line := range printed {
		if strings.Contains(line, "intent ledger") && strings.Contains(line, "captured") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ledger capture context, got %q", printed)
	}

	events, err := ls.Events("test-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 utterance event, got %d", len(events))
	}
	if events[0].Type != ledger.EventUtterance {
		t.Fatalf("expected utterance, got %s", events[0].Type)
	}
}

func TestTerminalPromptSkipsCaptureWhenLedgerDisabled(t *testing.T) {
	withoutSessionContextLimit(t)
	fake := &cliSurface{kind: surface.KindClaude}
	_, store := cliFixture(t, fake)

	projectDir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{
		ID: "proj-2", WorkspaceRoot: projectDir, Title: "Disabled", Key: "disabled-key",
	}); err != nil {
		t.Fatal(err)
	}
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	if err := ls.Enable("disabled-key"); err != nil {
		t.Fatal(err)
	}
	if err := ls.Disable("disabled-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterSession(surface.Session{
		ID: "s2", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	hooks := rootHooks(t, store, fake, 1)
	hooks(`{"session_id":"s2","hook_event_name":"SessionStart","source":"startup"}`)
	hooks(`{"session_id":"s2","hook_event_name":"UserPromptSubmit","prompt":"should not capture"}`)

	events, err := ls.Events("disabled-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events for disabled project, got %d", len(events))
	}
}

func TestTerminalPromptCapturesForCoordinatorInUnsetProject(t *testing.T) {
	withoutSessionContextLimit(t)
	fake := &cliSurface{kind: surface.KindClaude}
	_, store := cliFixture(t, fake)

	projectDir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{
		ID: "proj-3", WorkspaceRoot: projectDir, Title: "Unset", Key: "unset-key",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterSession(surface.Session{
		ID: "s3", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI,
		Cwd: projectDir, Status: surface.StatusIdle,
	}); err != nil {
		t.Fatal(err)
	}

	hooks := rootHooks(t, store, fake, 1)
	hooks(`{"session_id":"s3","hook_event_name":"SessionStart","source":"startup"}`)
	hooks(`{"session_id":"s3","hook_event_name":"UserPromptSubmit","prompt":"coordinator capture test"}`)

	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	events, err := ls.Events("unset-key", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event for unset (coordinator-captures) project, got %d", len(events))
	}
}

var _ = time.Now
