package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

func commandInserts(commands *surface.RuntimeCommands) []string {
	var out []string
	for _, command := range commands.Commands {
		out = append(out, command.Insert)
	}
	return out
}

func TestAHostSessionOffersTheCommandsItsClaudeReported(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	commands, err := c.Commands(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := commandInserts(commands), []string{"/review", "/compact", "/statusline", "/l5-probe"}; !slices.Equal(got, want) || commands.Unavailable != "" {
		t.Fatalf("commands = %v (unavailable %q), want %v", got, commands.Unavailable, want)
	}
	if commands.Commands[0].Description != "Review a pull request" || commands.Commands[0].ArgumentHint != "[pr]" {
		t.Fatalf("review = %+v, want Claude's own command for the shared name", commands.Commands[0])
	}
}

func TestASessionAPersonReachesOutsideTheHostOffersWhatClaudeListsForItsFolder(t *testing.T) {
	c, fixture := hostedClaude(t)
	fixture.Start(t)
	sess := &surface.Session{ID: uuid.NewString(), Surface: surface.KindClaude, Transport: "uds", Cwd: fixture.Cwd}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	commands, err := c.Commands(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if got := commandInserts(commands); !slices.Contains(got, "/l5-probe") || commands.Unavailable != "" {
		t.Fatalf("commands = %v (unavailable %q), want the folder's listing", got, commands.Unavailable)
	}
}

func TestASessionReachedOnlyOverItsPeerSocketSaysWhyItOffersNoCommands(t *testing.T) {
	c, _ := hostedClaude(t)
	c.localOnly = true
	sess := &surface.Session{ID: uuid.NewString(), Surface: surface.KindClaude, Transport: "uds", PID: os.Getpid(), Cwd: t.TempDir()}
	commands, err := c.Commands(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands.Commands) != 0 || commands.Unavailable != claudeCommandsUnavailable {
		t.Fatalf("commands = %+v, want none and the reason", commands)
	}
}

func TestAProcessTheHostStartsOnlyToListCommandsIsNoSession(t *testing.T) {
	c, fixture := hostedClaude(t)
	dir := filepath.Join(surface.ClaudeConfigDir(fixture.Home), "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	listing := claudehost.ListingSessionPrefix + "0123456789ab"
	record, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": listing, "cwd": fixture.Cwd, "kind": "interactive", "startedAt": time.Now().UnixMilli()})
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	_, records, err := c.records()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if str(record.fields, "sessionId") == listing {
			t.Fatalf("records include the listing process %+v", record.fields)
		}
	}
}

func TestAStreamEmitsASessionsCommandsOnlyWhenTheyChange(t *testing.T) {
	c, _ := hostedClaude(t)
	c.localOnly = true
	sess := &surface.Session{ID: uuid.NewString(), Surface: surface.KindClaude, Transport: "uds", PID: os.Getpid()}
	var events []surface.StreamEvent
	var stream claudeCommandsStream
	for range 2 {
		stream.emit(context.Background(), c, sess, func(event surface.StreamEvent) { events = append(events, event) })
	}
	if len(events) != 1 || events[0].Kind != "commands" || events[0].Commands.Unavailable != claudeCommandsUnavailable {
		t.Fatalf("events = %+v, want one commands event", events)
	}
}

func TestCodexOffersEachEnabledSkillOnceAsADollarCommand(t *testing.T) {
	resp := map[string]any{"result": map[string]any{"data": []any{map[string]any{"cwd": "/w", "skills": []any{
		map[string]any{"name": "review", "description": "Long review text", "interface": map[string]any{"shortDescription": "Review"}},
		map[string]any{"name": "off", "description": "Disabled", "enabled": false},
		map[string]any{"name": "review", "description": "A second root's copy"},
		map[string]any{"name": "notes", "description": "Take notes", "shortDescription": "Notes"},
	}}}}}
	commands := codexRuntimeCommands(resp)
	want := []surface.RuntimeCommand{{Name: "review", Description: "Review", Insert: "$review"}, {Name: "notes", Description: "Notes", Insert: "$notes"}}
	if !slices.Equal(commands.Commands, want) || commands.Unavailable != "" {
		t.Fatalf("commands = %+v, want %+v", commands.Commands, want)
	}
}
