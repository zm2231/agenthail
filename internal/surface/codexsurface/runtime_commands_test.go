package codexsurface

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfacetest"
)

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
	sess := managedRolloutSession(t, `{"type":"session_meta","payload":{"id":"thread"}}`)
	sess.TranscriptOffsetSet = true
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
			if got := surfacetest.CommandInserts(event.Commands); !slices.Equal(got, want) {
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
