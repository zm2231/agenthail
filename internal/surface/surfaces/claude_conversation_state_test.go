package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestClaudeConversationStateIsGoneOnlyOnPositiveEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *claudetest.Fixture)
		want  surface.ConversationState
	}{
		{name: "nothing anywhere", setup: func(t *testing.T, f *claudetest.Fixture) {}, want: surface.ConversationGone},
		{name: "a transcript and no process", setup: func(t *testing.T, f *claudetest.Fixture) {
			f.Transcript(fixtureConversation, f.Home)
		}, want: surface.ConversationResumable},
		{name: "a live process", setup: func(t *testing.T, f *claudetest.Fixture) {
			f.Transcript(fixtureConversation, f.Home)
			f.Record(fixtureConversation, f.Home, "")
		}, want: surface.ConversationLive},
		{name: "the daemon's root while the caller names another", setup: func(t *testing.T, f *claudetest.Fixture) {
			f.Transcript(fixtureConversation, f.Home)
			if err := surface.RecordDaemonClaudeRoot(f.Home); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", f.AddRoot("other-root"))
		}, want: surface.ConversationResumable},
		{name: "a job still starting in the daemon's root while the caller names another", setup: func(t *testing.T, f *claudetest.Fixture) {
			f.Job("bg-starting", fixtureConversation, f.Home, "starting", 0)
			if err := surface.RecordDaemonClaudeRoot(f.Home); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CONFIG_DIR", f.AddRoot("other-root"))
		}, want: surface.ConversationLive},
		{name: "a transcript in two roots", setup: func(t *testing.T, f *claudetest.Fixture) {
			f.Transcript(fixtureConversation, f.Home)
			f.AddRoot("second-root")
			f.Transcript(fixtureConversation, f.Home)
		}, want: surface.ConversationUnknown},
		{name: "a root whose projects cannot be read", setup: func(t *testing.T, f *claudetest.Fixture) {
			projects := filepath.Join(f.AddRoot("locked-root"), "projects")
			if err := os.MkdirAll(projects, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(projects, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(projects, 0o700) })
		}, want: surface.ConversationUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			f := claudetest.New(t)
			tc.setup(t, f)
			state, err := NewClaude(nil, f.Home).ConversationState(context.Background(), &surface.Session{ID: fixtureConversation})
			if state != tc.want {
				t.Fatalf("state=%s err=%v, want %s", state, err, tc.want)
			}
			if state == surface.ConversationUnknown && err == nil {
				t.Fatal("an unknown state carries no reason")
			}
		})
	}
}
