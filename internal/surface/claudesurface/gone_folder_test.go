package claudesurface

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func removedWorktreeSession(t *testing.T, roots func(worktree string) []string) (checkout, worktree string, session surface.Session, c *Claude, spawns func() int, hooks func() string) {
	t.Helper()
	fixture, host, c := hostResume(t)
	checkout = filepath.Join(fixture.Home, "work", "repo")
	worktree = filepath.Join(checkout, ".worktrees", "task")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	session = exitedSession(fixture)
	session.Cwd = worktree
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	c.BoundFolders(func(id string) ([]string, error) {
		if id != fixtureConversation {
			t.Errorf("bounds asked for %s", id)
		}
		return roots(worktree), nil
	})
	spawns = func() int { return len(host.Spawns(t, fixtureConversation)) }
	hooks = func() string {
		data, err := os.ReadFile(filepath.Join(host.ClaudeDir, "hooks.jsonl"))
		if err != nil {
			return ""
		}
		var started []string
		for line := range strings.Lines(string(data)) {
			var record struct {
				Event  string `json:"event"`
				Output struct {
					HookSpecificOutput struct {
						AdditionalContext string `json:"additionalContext"`
					} `json:"hookSpecificOutput"`
				} `json:"output"`
			}
			if json.Unmarshal([]byte(line), &record) == nil && record.Event == "SessionStart" {
				started = append(started, record.Output.HookSpecificOutput.AdditionalContext)
			}
		}
		return strings.Join(started, "\n")
	}
	return checkout, worktree, session, c, spawns, hooks
}

func TestAMessageToAWorkerWhoseWorktreeWasRemovedResumesItInItsCheckoutAndSaysSo(t *testing.T) {
	checkout, worktree, session, c, spawns, hooks := removedWorktreeSession(t, func(worktree string) []string { return []string{worktree} })
	result, err := c.SendHumanTurn(context.Background(), &session, "are you there?", nil)
	if err != nil || !result.Accepted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if want := "Its folder " + worktree + " is gone, so it resumed in " + checkout + "."; result.Notice != want {
		t.Fatalf("notice %q, want %q", result.Notice, want)
	}
	if spawns() != 1 || !samePath(t, session.Cwd, checkout) {
		t.Fatalf("spawns=%d session cwd %s, want one resume in %s", spawns(), session.Cwd, checkout)
	}
	line := (surface.FolderMove{Gone: worktree, Into: checkout, Checkout: true}).Line()
	if started := hooks(); !strings.Contains(started, line) {
		t.Fatalf("SessionStart told Claude %q, want %q", started, line)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("the removed worktree was recreated: %v", err)
	}
}

func TestAMessageToASessionWhoseFolderIsGoneAndWhoseRootsAreElsewhereIsRefusedWithoutAStart(t *testing.T) {
	_, worktree, session, c, spawns, _ := removedWorktreeSession(t, func(worktree string) []string {
		return []string{filepath.Join(filepath.Dir(worktree), "..", "..", "elsewhere")}
	})
	_, err := c.SendHumanTurn(context.Background(), &session, "are you there?", nil)
	if !surface.IsDeliveryTerminal(err) || !strings.Contains(err.Error(), "its folder "+worktree+" is gone") || !strings.Contains(err.Error(), "or archive the session") {
		t.Fatalf("err=%v, want a terminal refusal that says what to do", err)
	}
	if spawns() != 0 {
		t.Fatalf("spawns=%d, want none", spawns())
	}
}

func TestAMessageToASessionWhoseFolderIsGoneResumesItInTheFolderAboveWithItsTranscript(t *testing.T) {
	fixture, host, c := hostResume(t)
	parent := t.TempDir()
	gone := filepath.Join(parent, "removed")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	session := exitedSession(fixture)
	session.Cwd = gone
	session.Transcript = fixture.Transcript(fixtureConversation, gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	result, err := c.SendHumanTurn(surface.WithMessageUUID(context.Background(), uuid.NewString()), &session, "are you there", nil)
	if err != nil || !result.Accepted || result.Notice != "Its folder "+gone+" is gone, so it resumed in "+parent+"." {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 || !samePath(t, spawns[0].Cwd, parent) {
		t.Fatalf("spawns=%+v, want one resume in %s", spawns, parent)
	}
	testkit.Eventually(t, "the turn", 15*time.Second, func() bool {
		turns := host.Turns(t, fixtureConversation)
		return len(turns) == 1 && turns[0].Text == "are you there"
	})
}
