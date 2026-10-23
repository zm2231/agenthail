package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/codextest"
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

func TestACommandsWatchListsOffTheStreamAndSendsOnlyAChangedList(t *testing.T) {
	release := make(chan *surface.RuntimeCommands)
	asked := make(chan struct{}, 8)
	var lists atomic.Int32
	events := make(chan surface.StreamEvent, 8)
	started := time.Now()
	watch := watchCommands(context.Background(), func(ctx context.Context) (*surface.RuntimeCommands, error) {
		lists.Add(1)
		asked <- struct{}{}
		select {
		case commands := <-release:
			return commands, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, func(event surface.StreamEvent) { events <- event })
	if waited := time.Since(started); waited > 100*time.Millisecond {
		t.Fatalf("starting the watch waited %s for a list", waited)
	}
	<-asked
	for range 3 {
		watch.Refresh()
	}
	review := &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{{Name: "review", Insert: "/review"}}}
	release <- review
	if event := <-events; event.Kind != "commands" || event.Version != 1 || !sameRuntimeCommands(event.Commands, review) {
		t.Fatalf("event = %+v", event)
	}
	release <- &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{{Name: "review", Insert: "/review"}}}
	watch.Refresh()
	notes := &surface.RuntimeCommands{Commands: []surface.RuntimeCommand{{Name: "notes", Insert: "/notes"}}}
	release <- notes
	if event := <-events; event.Version != 2 || !sameRuntimeCommands(event.Commands, notes) {
		t.Fatalf("event = %+v, want only the changed list", event)
	}
	if got := lists.Load(); got != 3 {
		t.Fatalf("lists = %d, want asks made during a list folded into one", got)
	}
	watch.Refresh()
	stopped := make(chan struct{})
	go func() {
		watch.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping waited on a list in flight")
	}
	if len(events) != 0 {
		t.Fatalf("events after stop: %+v", <-events)
	}
}

func TestAClaudeStreamSendsTheTimelineWhileItsFolderIsStillBeingListed(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_INITIALIZE_MS", "3000")
	c, fixture := hostedClaude(t)
	fixture.Start(t)
	id := uuid.NewString()
	path := filepath.Join(fixture.Home, ".claude", "projects", "a-folder", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","uuid":"u1","message":{"content":"say hello"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"the first reply"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	sess := &surface.Session{ID: id, Surface: surface.KindClaude, Transport: "uds", Cwd: fixture.Cwd, Transcript: path, TranscriptFromStart: true}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	events := make(chan surface.StreamEvent, 64)
	streamed := make(chan error, 1)
	started := time.Now()
	go func() {
		streamed <- c.Stream(ctx, sess, "", func(event surface.StreamEvent) { events <- event }, 15*time.Second)
	}()
	var replied time.Duration
	for {
		select {
		case event := <-events:
			if event.Text == "the first reply" && replied == 0 {
				if replied = time.Since(started); replied > 2*time.Second {
					t.Fatalf("the reply waited %s behind the folder's listing", replied)
				}
			}
			if event.Kind == "commands" {
				if replied == 0 {
					t.Fatal("the commands came before the timeline they were listed beside")
				}
				if !slices.Contains(commandInserts(event.Commands), "/l5-probe") {
					t.Fatalf("commands = %v", commandInserts(event.Commands))
				}
				cancel()
				<-streamed
				return
			}
		case err := <-streamed:
			t.Fatalf("the stream ended before the folder's commands: %v", err)
		case <-ctx.Done():
			t.Fatal("the folder's commands never reached the stream")
		}
	}
}

func TestACodexStreamRelistsSkillsWhenTheAppServerSaysTheyChanged(t *testing.T) {
	slowManagedPoll(t)
	var listed atomic.Int32
	fake := startManagedCodex(t, func(method string, params map[string]any) map[string]any {
		if method == "skills/list" {
			skills := []any{map[string]any{"name": "review", "description": "Review"}}
			if listed.Add(1) > 1 {
				skills = append(skills, map[string]any{"name": "found-later", "description": "Found later"})
			}
			return map[string]any{"data": []any{map[string]any{"cwd": "/w", "skills": skills}}}
		}
		return (&switchableTurn{}).respond(method, params)
	})
	fake.AddThread(codextest.Thread{ID: "thread"})
	fake.Load("thread")
	sess := managedSession()
	sess.Cwd = "/w"
	events := make(chan surface.StreamEvent, 64)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = isolatedManagedRuntime(t).Stream(ctx, sess, "", func(event surface.StreamEvent) {
			if event.Kind == "commands" {
				events <- event
			}
		}, time.Minute)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	awaitInserts := func(want ...string) {
		t.Helper()
		select {
		case event := <-events:
			if got := commandInserts(event.Commands); !slices.Equal(got, want) {
				t.Fatalf("commands = %v, want %v", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no commands event, want %v", want)
		}
	}
	awaitInserts("$review")
	awaitCalls(t, fake, "thread/resume", 1)
	fake.Notify("skills/changed", map[string]any{})
	awaitInserts("$review", "$found-later")
	if params := fake.Calls("skills/list")[0]; params["forceReload"] != true {
		t.Fatalf("skills/list params = %v", params)
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
