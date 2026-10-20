package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func signIn(t *testing.T, path, account string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"accountUuid": account, "emailAddress": account + "@example.com"}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

// moveFixture is a conversation in the primary root, a signed-in second root
// named "spare", and a Claude host that can resume the conversation.
func moveFixture(t *testing.T) (*claudetest.Fixture, *claudehosttest.Fixture, *Claude, string, string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture, host, c := hostResume(t)
	primary := fixture.Root
	spare := fixture.AddRoot("spare")
	fixture.Root = primary
	signIn(t, filepath.Join(fixture.Home, ".claude.json"), "one")
	signIn(t, filepath.Join(spare, ".claude.json"), "two")
	c.mcpAdder = func(context.Context, string, string, string, json.RawMessage) error { return nil }
	return fixture, host, c, primary, spare
}

func TestMoveMakesAnExitedConversationLiveInTheTargetRootAndLeavesItExited(t *testing.T) {
	fixture, host, c, primary, spare := moveFixture(t)
	transcript := fixture.Transcript(fixtureConversation, fixture.Home)
	subagent := filepath.Join(primary, "projects", "fixture-project", fixtureConversation, "subagents", "agent-a.jsonl")
	env := filepath.Join(primary, "session-env", fixtureConversation, "hook.sh")
	for _, path := range []string{subagent, env} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	original, _ := os.ReadFile(transcript)
	trusted, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"accountUuid": "one"}, "projects": map[string]any{fixture.Home: map[string]any{"hasTrustDialogAccepted": true}}})
	if err := os.WriteFile(filepath.Join(fixture.Home, ".claude.json"), trusted, 0600); err != nil {
		t.Fatal(err)
	}

	result, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation, Surface: surface.KindClaude}, surface.MoveRequest{To: surface.MoveTarget{Account: "spare"}})
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(spare, "projects", "fixture-project", fixtureConversation+".jsonl")
	if got, _ := os.ReadFile(moved); string(got) != string(original) {
		t.Fatalf("target transcript = %q", got)
	}
	if _, err := os.Stat(transcript); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the source transcript is still live: %v", err)
	}
	if _, err := os.Stat(transcript + claudeMovedSuffix); err != nil {
		t.Fatalf("the source was not kept as a recovery copy: %v", err)
	}
	for _, piece := range []string{filepath.Join("projects", "fixture-project", fixtureConversation, "subagents", "agent-a.jsonl"), filepath.Join("session-env", fixtureConversation, "hook.sh")} {
		if _, err := os.Stat(filepath.Join(spare, piece)); err != nil {
			t.Fatalf("%s did not move: %v", piece, err)
		}
	}
	if root, err := c.conversationRoot(fixtureConversation); err != nil || root != spare {
		t.Fatalf("conversation root = %q err=%v", root, err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("spawns=%+v, want an exited conversation left exited", spawns)
	}
	if result.Session == nil || result.Session.Transcript != moved || result.FromRoot != primary || result.ToRoot != spare || !result.Copied || result.SameAccount {
		t.Fatalf("result=%+v session=%+v", result, result.Session)
	}
	var carried struct {
		Projects map[string]struct {
			Trust bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	data, _ := os.ReadFile(filepath.Join(spare, ".claude.json"))
	if json.Unmarshal(data, &carried) != nil || !carried.Projects[fixture.Home].Trust {
		t.Fatalf("the folder's trust did not reach the target root: %s", data)
	}
	journal, err := c.readMoveJournal(fixtureConversation)
	if err != nil || journal == nil || journal.Phase != claudeMovePhaseDone || journal.Retired.IsZero() {
		t.Fatalf("journal=%+v err=%v", journal, err)
	}

	again, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation, Surface: surface.KindClaude}, surface.MoveRequest{To: surface.MoveTarget{Account: "spare"}})
	if err != nil || !again.Unchanged {
		t.Fatalf("repeating the move: result=%+v err=%v", again, err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("repeating the move resumed the conversation: %+v", spawns)
	}
	if pruned, err := PruneClaudeMoves(c.home, ClaudeMoveRetention, time.Now()); err != nil || len(pruned) != 0 {
		t.Fatalf("a fresh recovery copy was pruned: %v %v", pruned, err)
	}
	if pruned, err := PruneClaudeMoves(c.home, ClaudeMoveRetention, time.Now().Add(ClaudeMoveRetention+time.Hour)); err != nil || len(pruned) != 1 {
		t.Fatalf("pruned=%v err=%v", pruned, err)
	}
	if _, err := os.Stat(transcript + claudeMovedSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the recovery copy outlived its retention: %v", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("pruning touched the live transcript: %v", err)
	}
}

func TestMoveRefusesABusySessionAndCopiesNothing(t *testing.T) {
	for status, want := range map[string]string{"busy": "a turn is running", "waiting": "waiting on a question", "shell": "background task"} {
		t.Run(status, func(t *testing.T) {
			fixture, _, c, primary, spare := moveFixture(t)
			fixture.Transcript(fixtureConversation, fixture.Home)
			fixture.RecordStatus(fixtureConversation, fixture.Home, "", status, "", time.Time{})
			_, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.MoveRequest{To: surface.MoveTarget{Account: "spare"}})
			var busy *surface.MoveBusyError
			if !errors.As(err, &busy) || !strings.Contains(busy.Reason, want) {
				t.Fatalf("err=%v", err)
			}
			if _, err := os.Stat(filepath.Join(spare, "projects")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a refused move copied into the target: %v", err)
			}
			if root, _ := c.conversationRoot(fixtureConversation); root != primary {
				t.Fatalf("root=%s", root)
			}
		})
	}
}

func TestMoveStopsAnIdleTerminalProcessOnlyWithConsent(t *testing.T) {
	fixture, _, c, _, spare := moveFixture(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.Record(fixtureConversation, fixture.Home, "")
	request := surface.MoveRequest{To: surface.MoveTarget{Account: "spare"}}
	if _, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, request); err == nil || !strings.Contains(err.Error(), "open in a terminal") {
		t.Fatalf("a terminal process was stopped without consent: %v", err)
	}
	if !claudeProcessAlive(fixture.PID) {
		t.Fatal("the terminal process was stopped without consent")
	}
	request.StopTerminal = true
	result, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Stopped, "terminal process") || result.ToRoot != spare {
		t.Fatalf("result=%+v", result)
	}
}

func TestAnInterruptedMoveContinuesFromItsJournal(t *testing.T) {
	fixture, host, c, primary, spare := moveFixture(t)
	transcript := fixture.Transcript(fixtureConversation, fixture.Home)
	journal := &claudeMoveJournal{Conversation: fixtureConversation, From: primary, To: spare, ProjectDir: "fixture-project", Cwd: fixture.Home, Started: time.Now()}
	journal.Pieces = claudeConversationPieces(primary, journal.ProjectDir, fixtureConversation)
	if err := c.stageMove(journal); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(transcript, transcript+claudeMovedSuffix); err != nil {
		t.Fatal(err)
	}
	if root, err := c.conversationRoot(fixtureConversation); err != nil || root != "" {
		t.Fatalf("mid-switch the conversation is in %q (err %v), want neither root", root, err)
	}
	if _, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.MoveRequest{To: surface.MoveTarget{Account: "default"}}); err == nil || !strings.Contains(err.Error(), "unfinished move") {
		t.Fatalf("a different target was accepted mid-move: %v", err)
	}
	result, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.MoveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if root, _ := c.conversationRoot(fixtureConversation); root != spare || result.ToRoot != spare {
		t.Fatalf("root=%s result=%+v", root, result)
	}
	if result.Session == nil || result.Session.Transcript != filepath.Join(spare, "projects", "fixture-project", fixtureConversation+".jsonl") {
		t.Fatalf("session=%+v", result.Session)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("spawns=%+v", spawns)
	}
}

func TestMoveStopsARunningHostSessionAndResumesItInTheTargetRoot(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, "")
	claudetest.New(t)
	host.Start(t)
	c := NewClaude(nil, host.Home)
	c.mcpAdder = func(context.Context, string, string, string, json.RawMessage) error { return nil }
	spare := filepath.Join(host.Home, "spare")
	signIn(t, filepath.Join(host.Home, ".claude.json"), "one")
	signIn(t, filepath.Join(spare, ".claude.json"), "two")
	if err := os.MkdirAll(filepath.Join(host.Home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(host.Home, ".agenthail", "config.json"), []byte(`{"claudeConfigDirs":["`+spare+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	session, _, err := c.StartSession(ctx, surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	listed := listedSession(t, c, session.ID)
	result, err := c.MoveSession(ctx, listed, surface.MoveRequest{To: surface.MoveTarget{Account: spare}, Wait: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stopped != "host" || result.ToRoot != spare || result.Session == nil || !claudeHosted(result.Session) || claudeTranscriptRoot(result.Session.Transcript) != spare {
		t.Fatalf("result=%+v session=%+v", result, result.Session)
	}
	spawns := host.Spawns(t, session.ID)
	if len(spawns) != 2 || spawns[1].ConfigDir == nil || *spawns[1].ConfigDir != spare {
		t.Fatalf("spawns=%+v, want the start and one resume in %s", spawns, spare)
	}
}

func TestMoveRefusesASignedOutTargetAndWarnsOnASharedAccount(t *testing.T) {
	fixture, _, c, _, spare := moveFixture(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	if err := os.Remove(filepath.Join(spare, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.MoveRequest{To: surface.MoveTarget{Account: "spare"}}); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("err=%v", err)
	}
	signIn(t, filepath.Join(spare, ".claude.json"), "one")
	result, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.MoveRequest{To: surface.MoveTarget{Account: "spare"}})
	if err != nil || !result.SameAccount || len(result.Warnings) == 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAdoptingATerminalConversationResumesItInTheHostInItsOwnRoot(t *testing.T) {
	fixture, host, c, primary, _ := moveFixture(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.Record(fixtureConversation, fixture.Home, "")
	result, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.MoveRequest{StopTerminal: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Copied || result.ToRoot != primary || result.Session == nil || !claudeHosted(result.Session) {
		t.Fatalf("result=%+v", result)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 {
		t.Fatalf("spawns=%+v", spawns)
	}
	again, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.MoveRequest{})
	if err != nil || !again.Unchanged {
		t.Fatalf("adopting a hosted conversation again: result=%+v err=%v", again, err)
	}
}

func TestAStagedMoveRechecksAHolderThatCameBackBeforeSwitching(t *testing.T) {
	fixture, host, c, primary, spare := moveFixture(t)
	transcript := fixture.Transcript(fixtureConversation, fixture.Home)
	journal := &claudeMoveJournal{Conversation: fixtureConversation, From: primary, To: spare, ProjectDir: "fixture-project", Cwd: fixture.Home, Resume: true, Started: time.Now()}
	journal.Pieces = claudeConversationPieces(primary, journal.ProjectDir, fixtureConversation)
	if err := c.stageMove(journal); err != nil {
		t.Fatal(err)
	}
	later := `{"type":"user","message":{"role":"user","content":"written after the copy"}}` + "\n"
	appendFile(t, transcript, later)
	fixture.RecordStatus(fixtureConversation, fixture.Home, "", "busy", "", time.Time{})
	_, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.MoveRequest{})
	var busy *surface.MoveBusyError
	if !errors.As(err, &busy) {
		t.Fatalf("a staged move went on past a busy holder: %v", err)
	}
	if root, _ := c.conversationRoot(fixtureConversation); root != primary {
		t.Fatalf("root=%s", root)
	}
	fixture.RemoveRecord()
	result, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.MoveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := os.ReadFile(filepath.Join(spare, "projects", "fixture-project", fixtureConversation+".jsonl"))
	if err != nil || !strings.Contains(string(moved), "written after the copy") {
		t.Fatalf("the move promoted a stale copy (err %v)", err)
	}
	if result.ToRoot != spare || len(host.Spawns(t, fixtureConversation)) != 1 {
		t.Fatalf("the conversation that was running before the move was not resumed: result=%+v", result)
	}
}

func TestAFinishedSwitchDoesNotResumeAConversationAlreadyHeldInTheTarget(t *testing.T) {
	fixture, host, c, primary, spare := moveFixture(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	journal := &claudeMoveJournal{Conversation: fixtureConversation, From: primary, To: spare, ProjectDir: "fixture-project", Cwd: fixture.Home, Resume: true, Started: time.Now()}
	journal.Pieces = claudeConversationPieces(primary, journal.ProjectDir, fixtureConversation)
	if err := c.stageMove(journal); err != nil {
		t.Fatal(err)
	}
	for _, piece := range journal.Pieces {
		if err := os.Rename(filepath.Join(primary, piece), filepath.Join(primary, piece)+claudeMovedSuffix); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(spare, piece)+claudeStagedSuffix, filepath.Join(spare, piece)); err != nil {
			t.Fatal(err)
		}
	}
	journal.Phase = claudeMovePhaseSwitched
	if err := c.writeMoveJournal(journal); err != nil {
		t.Fatal(err)
	}
	fixture.Record(fixtureConversation, fixture.Home, "")
	result, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.MoveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("a held conversation was resumed a second time: %+v", spawns)
	}
	if result.Session == nil || result.Session.PID != fixture.PID {
		t.Fatalf("session=%+v", result.Session)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func TestAMoveGivesTheSourceBackWhenATerminalResumesItMidMove(t *testing.T) {
	for _, step := range []string{"staged", "retired"} {
		for _, busy := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/holder=%v", step, busy), func(t *testing.T) {
				fixture, _, c, primary, spare := moveFixture(t)
				transcript := fixture.Transcript(fixtureConversation, fixture.Home)
				c.moveStep = func(at string) {
					if at != step {
						return
					}
					c.moveStep = nil
					written := transcript
					if step == "retired" {
						written += claudeMovedSuffix
					}
					appendFile(t, written, `{"type":"user","message":{"role":"user","content":"written mid-move"}}`+"\n")
					if busy {
						fixture.RecordStatus(fixtureConversation, fixture.Home, "", "busy", "", time.Time{})
					}
				}
				request := surface.MoveRequest{To: surface.MoveTarget{Account: "spare"}}
				_, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, request)
				var refused *surface.MoveBusyError
				if !errors.As(err, &refused) {
					t.Fatalf("a move switched past a terminal that resumed mid-move: %v", err)
				}
				if root, _ := c.conversationRoot(fixtureConversation); root != primary {
					t.Fatalf("root=%s", root)
				}
				if content, _ := os.ReadFile(transcript); !strings.Contains(string(content), "written mid-move") {
					t.Fatal("the source lost the write made mid-move")
				}
				if busy {
					fixture.RemoveRecord()
				}
				if _, err := c.MoveSession(context.Background(), &surface.Session{ID: fixtureConversation}, request); err != nil {
					t.Fatal(err)
				}
				moved, _ := os.ReadFile(filepath.Join(spare, "projects", "fixture-project", fixtureConversation+".jsonl"))
				if !strings.Contains(string(moved), "written mid-move") {
					t.Fatal("the retried move lost the write made mid-move")
				}
			})
		}
	}
}
