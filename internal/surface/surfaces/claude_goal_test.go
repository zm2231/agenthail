package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const (
	goalSetRecord     = `{"type":"attachment","uuid":"g1","timestamp":"2026-10-20T16:07:30.609Z","attachment":{"type":"goal_status","met":false,"sentinel":true,"condition":"tests pass"}}`
	goalUnmetRecord   = `{"type":"attachment","uuid":"g2","timestamp":"2026-10-20T16:07:40.000Z","attachment":{"type":"goal_status","met":false,"condition":"tests pass","reason":"only one test ran"}}`
	goalMetRecord     = `{"type":"attachment","uuid":"g3","timestamp":"2026-10-20T16:08:30.609Z","attachment":{"type":"goal_status","met":true,"condition":"tests pass","reason":"go test exited 0","iterations":2,"durationMs":60000,"tokens":835}}`
	goalFailedRecord  = `{"type":"attachment","uuid":"g4","timestamp":"2026-10-20T16:08:30.609Z","attachment":{"type":"goal_status","met":false,"failed":true,"condition":"tests pass","reason":"the test binary cannot be built here","iterations":1,"durationMs":3000,"tokens":90}}`
	goalClearedRecord = `{"type":"attachment","uuid":"g5","timestamp":"2026-10-20T16:09:00.000Z","attachment":{"type":"goal_status","met":true,"sentinel":true,"condition":"tests pass"}}`
	goalPausedNotice  = `{"type":"system","subtype":"informational","level":"notice","content":"Goal paused · goal checks kept finding it unmet this turn · send a message to continue"}`
)

func readClaudeGoal(t *testing.T, claude *Claude, path string) *surface.GoalState {
	t.Helper()
	goal, err := claude.GoalGet(context.Background(), &surface.Session{ID: "goal-session", Transcript: path})
	if err != nil {
		t.Fatal(err)
	}
	return goal
}

func TestClaudeGoalStatusFollowsTheTranscriptsGoalRecords(t *testing.T) {
	set, _ := time.Parse(time.RFC3339Nano, "2026-10-20T16:07:30.609Z")
	cases := []struct {
		name    string
		records []string
		want    *surface.GoalState
	}{
		{"none", []string{`{"type":"user","uuid":"u1","message":{"content":"hello"}}`}, nil},
		{"set", []string{goalSetRecord}, &surface.GoalState{Objective: "tests pass", Status: surface.GoalStatusActive, CreatedAt: set}},
		{"unmet", []string{goalSetRecord, goalUnmetRecord}, &surface.GoalState{Objective: "tests pass", Status: surface.GoalStatusActive, Reason: "only one test ran", TimeUsedSeconds: 9, CreatedAt: set}},
		{"met", []string{goalSetRecord, goalUnmetRecord, goalMetRecord}, &surface.GoalState{Objective: "tests pass", Status: surface.GoalStatusComplete, Reason: "go test exited 0", TimeUsedSeconds: 60, TokensUsed: 835, CreatedAt: set}},
		{"impossible", []string{goalSetRecord, goalFailedRecord}, &surface.GoalState{Objective: "tests pass", Status: surface.GoalStatusBlocked, Reason: "the test binary cannot be built here", TimeUsedSeconds: 3, TokensUsed: 90, CreatedAt: set}},
		{"cleared", []string{goalSetRecord, goalUnmetRecord, goalClearedRecord}, nil},
		{"paused notice leaves it active", []string{goalSetRecord, goalUnmetRecord, goalPausedNotice}, &surface.GoalState{Objective: "tests pass", Status: surface.GoalStatusActive, Reason: "only one test ran", TimeUsedSeconds: 9, CreatedAt: set}},
		{"quoted record text is not a goal", []string{`{"type":"user","uuid":"u1","message":{"content":"{\"type\":\"goal_status\",\"met\":true}"}}`}, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			goal := readClaudeGoal(t, NewClaude(nil, t.TempDir()), writeTranscript(t, strings.Join(test.records, "\n")))
			if test.want == nil {
				if goal != nil {
					t.Fatalf("goal=%+v, want none", goal)
				}
				return
			}
			if goal == nil {
				t.Fatalf("no goal, want %+v", test.want)
			}
			goal.UpdatedAt = time.Time{}
			if *goal != *test.want {
				t.Fatalf("goal=%+v\nwant=%+v", goal, test.want)
			}
		})
	}
}

func TestClaudeGoalSetAgainForTheActiveGoalKeepsItsStart(t *testing.T) {
	again := strings.Replace(strings.Replace(goalSetRecord, `"g1"`, `"g1b"`, 1), "16:07:30.609Z", "16:20:00.000Z", 1)
	goal := readClaudeGoal(t, NewClaude(nil, t.TempDir()), writeTranscript(t, goalSetRecord+"\n"+goalUnmetRecord+"\n"+again))
	if goal == nil || goal.Status != surface.GoalStatusActive || goal.CreatedAt.Format(time.RFC3339Nano) != "2026-10-20T16:07:30.609Z" {
		t.Fatalf("goal=%+v", goal)
	}
	next := strings.Replace(again, `"condition":"tests pass"`, `"condition":"lint is clean"`, 1)
	goal = readClaudeGoal(t, NewClaude(nil, t.TempDir()), writeTranscript(t, goalSetRecord+"\n"+goalMetRecord+"\n"+next))
	if goal == nil || goal.Objective != "lint is clean" || goal.Status != surface.GoalStatusActive || goal.Reason != "" || goal.CreatedAt.Format(time.RFC3339Nano) != "2026-10-20T16:20:00Z" {
		t.Fatalf("a new goal after completion = %+v", goal)
	}
}

func TestClaudeGoalReadsOnlyWhatClaudeAppends(t *testing.T) {
	path := writeTranscript(t, goalSetRecord)
	claude := NewClaude(nil, t.TempDir())
	if goal := readClaudeGoal(t, claude, path); goal == nil || goal.Status != surface.GoalStatusActive {
		t.Fatalf("goal=%+v", goal)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	partial := goalMetRecord[:40]
	if _, err := file.WriteString(partial); err != nil {
		t.Fatal(err)
	}
	if goal := readClaudeGoal(t, claude, path); goal == nil || goal.Status != surface.GoalStatusActive {
		t.Fatalf("partial record changed the goal: %+v", goal)
	}
	if _, err := file.WriteString(goalMetRecord[40:] + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()
	goal := readClaudeGoal(t, claude, path)
	if goal == nil || goal.Status != surface.GoalStatusComplete {
		t.Fatalf("goal=%+v", goal)
	}
	goal.Objective = "changed by the caller"
	if again := readClaudeGoal(t, claude, path); again.Objective != "tests pass" {
		t.Fatalf("cached goal shared with a caller: %+v", again)
	}
	if err := os.WriteFile(path, []byte(goalSetRecord+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if goal := readClaudeGoal(t, claude, path); goal == nil || goal.Status != surface.GoalStatusActive {
		t.Fatalf("rewritten transcript kept the old goal: %+v", goal)
	}
}

type streamedGoal struct {
	goal    *surface.GoalState
	version uint64
}

func streamClaudeGoals(t *testing.T, path string) (<-chan streamedGoal, <-chan string, context.CancelFunc) {
	t.Helper()
	session := seededClaudeStreamSession(t, path, "")
	goals := make(chan streamedGoal, 16)
	texts := make(chan string, 64)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewClaude(nil, t.TempDir()).Stream(ctx, session, "", func(event surface.StreamEvent) {
			switch {
			case event.Kind == "goal":
				goals <- streamedGoal{event.Goal, event.Version}
			case event.Text != "":
				texts <- event.Text
			}
		}, 10*time.Second)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return goals, texts, cancel
}

func nextStreamedGoal(t *testing.T, goals <-chan streamedGoal) streamedGoal {
	t.Helper()
	select {
	case goal := <-goals:
		return goal
	case <-time.After(5 * time.Second):
		t.Fatal("no goal event")
		return streamedGoal{}
	}
}

func awaitStreamedText(t *testing.T, texts <-chan string, want string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case text := <-texts:
			if text == want {
				return
			}
		case <-deadline:
			t.Fatalf("no timeline event %q", want)
		}
	}
}

func replaceTranscript(t *testing.T, path string, records ...string) {
	t.Helper()
	replacement := path + ".next"
	if err := os.WriteFile(replacement, []byte(strings.Join(records, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeStreamSendsTheGoalWhenClaudeRecordsAVerdict(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"/goal tests pass"}}`+"\n"+goalSetRecord)
	goals, _, _ := streamClaudeGoals(t, path)
	set := nextStreamedGoal(t, goals)
	if set.goal == nil || set.goal.Status != surface.GoalStatusActive {
		t.Fatalf("first goal event = %+v", set.goal)
	}
	appendTestTranscript(t, path, `{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}}`)
	appendTestTranscript(t, path, goalMetRecord)
	met := nextStreamedGoal(t, goals)
	if met.goal == nil || met.goal.Status != surface.GoalStatusComplete || met.goal.Reason != "go test exited 0" || met.version <= set.version {
		t.Fatalf("goal event = %+v version %d after %d", met.goal, met.version, set.version)
	}
	appendTestTranscript(t, path, `{"type":"user","uuid":"u2","message":{"content":"thanks"}}`)
	select {
	case again := <-goals:
		t.Fatalf("a record without a goal change sent %+v", again.goal)
	case <-time.After(10 * transcriptPollInterval):
	}
}

func TestClaudeStreamSendsNoGoalForASessionWithoutOne(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"hello"}}`)
	goals, texts, _ := streamClaudeGoals(t, path)
	appendTestTranscript(t, path, `{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"hi"}]}}`)
	awaitStreamedText(t, texts, "hi")
	select {
	case goal := <-goals:
		t.Fatalf("goal event %+v for a session without a goal", goal.goal)
	case <-time.After(5 * transcriptPollInterval):
	}
}

func TestClaudeStreamFollowsAReplacedTranscript(t *testing.T) {
	padding := `{"type":"assistant","uuid":"pad","message":{"id":"mpad","content":[{"type":"text","text":"` + strings.Repeat("x", 2000) + `"}]}}`
	for name, records := range map[string][]string{
		"larger":  {`{"type":"user","uuid":"r1","message":{"content":"after the replacement"}}`, goalClearedRecord, padding},
		"shorter": {`{"type":"user","uuid":"r1","message":{"content":"after the replacement"}}`, goalClearedRecord},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"/goal tests pass"}}`+"\n"+goalSetRecord+"\n"+goalUnmetRecord)
			original, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			goals, texts, _ := streamClaudeGoals(t, path)
			active := nextStreamedGoal(t, goals)
			if active.goal == nil || active.goal.Status != surface.GoalStatusActive {
				t.Fatalf("goal event = %+v", active.goal)
			}
			replaceTranscript(t, path, records...)
			replaced, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if larger := replaced.Size() > original.Size(); larger != (name == "larger") {
				t.Fatalf("replacement is %d bytes after %d", replaced.Size(), original.Size())
			}
			cleared := nextStreamedGoal(t, goals)
			if cleared.goal != nil || cleared.version <= active.version {
				t.Fatalf("goal event = %+v version %d after %d", cleared.goal, cleared.version, active.version)
			}
			awaitStreamedText(t, texts, "after the replacement")
		})
	}
}

func TestClaudeReportsGoalStatusWithoutGoalControls(t *testing.T) {
	capabilities := NewClaude(nil, t.TempDir()).Capabilities()
	if !capabilities.GoalStatus || capabilities.Goal {
		t.Fatalf("capabilities=%+v", capabilities)
	}
}

func idleGoalSession(t *testing.T) (*claudetest.Fixture, *surface.Session) {
	fixture := claudetest.New(t)
	fixture.RecordStatus("conversation-test", fixture.Home, "session_test", "idle", "", time.Time{})
	path := writeTranscript(t, `{"type":"user","uuid":"u1","timestamp":"2026-10-20T16:00:00Z","message":{"content":"hello"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-10-20T16:00:01Z","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"hi"}]}}
{"type":"system","subtype":"stop_hook_summary"}`)
	return fixture, &surface.Session{ID: "conversation-test", Surface: surface.KindClaude, Bridge: "session_test", PID: fixture.PID, Status: surface.StatusIdle, Transcript: path}
}

func goalCommandOutput(args, output string) string {
	record, _ := json.Marshal(map[string]any{"type": "system", "subtype": "local_command", "uuid": newUUID(), "commandRun": map[string]any{"command": "goal", "args": args}, "content": "<local-command-stdout>" + output + "</local-command-stdout>"})
	return string(record) + "\n"
}

func postedText(payload map[string]any) string {
	message, _ := payload["message"].(map[string]any)
	text, _ := message["content"].(string)
	return text
}

func TestClaudeGoalCommandIsWhatClaudeAccepts(t *testing.T) {
	fixture, session := idleGoalSession(t)
	claude := NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home)
	for objective, want := range map[string]string{"  tests pass \n": "/goal tests pass", "": "/goal clear", strings.Repeat("é", claudeGoalConditionLimit): "/goal " + strings.Repeat("é", claudeGoalConditionLimit)} {
		if command, err := claude.GoalCommand(session, objective); err != nil || command != want {
			t.Fatalf("GoalCommand(%.20q) = %.30q, %v", objective, command, err)
		}
	}
	for _, objective := range []string{"Stop", "cancel", strings.Repeat("😀", claudeGoalConditionLimit/2+1)} {
		if command, err := claude.GoalCommand(session, objective); surface.DeliveryTerminalReason(err) != surface.DeliveryInvalidRequest {
			t.Fatalf("GoalCommand(%.20q) = %q, %v", objective, command, err)
		}
	}
	peer := &surface.Session{ID: "conversation-test", Surface: surface.KindClaude, Transport: "uds", PID: fixture.PID, Transcript: session.Transcript}
	if _, err := claude.GoalCommand(peer, "tests pass"); !errors.Is(err, errClaudeGoalPeer) {
		t.Fatalf("peer socket err=%v", err)
	}
}

func TestClaudeGoalSetIsOfferedWhereClaudeRunsCommands(t *testing.T) {
	fixture, session := idleGoalSession(t)
	claude := NewClaude(signin.Fixed{Browser: signin.Chrome}, fixture.Home)
	hosted := &surface.Session{ID: "hosted", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Transport: "uds", PID: fixture.PID}
	peer := &surface.Session{ID: "peer", Surface: surface.KindClaude, Transport: "uds", PID: fixture.PID}
	if !claude.SessionCapabilities(session).GoalSet || !claude.SessionCapabilities(hosted).GoalSet || claude.SessionCapabilities(peer).GoalSet {
		t.Fatal("goalSet must follow where /goal runs as a command")
	}
	if capabilities := claude.SessionCapabilities(session); capabilities.Goal {
		t.Fatalf("Claude has no goal pause or budget: %+v", capabilities)
	}
}

func TestClaudeGoalRunsAsACommandAndReportsClaudesAnswer(t *testing.T) {
	shortControlPoll(t)
	cases := []struct {
		name, objective, args, output string
		clear                         bool
		check                         func(error) bool
	}{
		{name: "set", objective: "tests pass", args: "tests pass", output: "Goal set: tests pass", check: func(err error) bool { return err == nil }},
		{name: "cleared", clear: true, args: "clear", output: "Goal cleared: tests pass", check: func(err error) bool { return err == nil }},
		{name: "nothing to clear", clear: true, args: "clear", output: "No goal set", check: func(err error) bool { return err == nil }},
		{name: "refused", objective: "tests pass", args: "tests pass", output: "Hooks are disabled by policy", check: func(err error) bool {
			return surface.DeliveryTerminalReason(err) == surface.DeliveryInvalidRequest && strings.Contains(err.Error(), "Hooks are disabled by policy")
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture, session := idleGoalSession(t)
			remote := newFakeRemoteControl(t)
			remote.onPost = func(payload map[string]any) []map[string]any {
				appendTranscript(t, session.Transcript, goalCommandOutput("other", "Goal set: other")+goalCommandOutput(test.args, test.output))
				return nil
			}
			claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
			var err error
			if test.clear {
				err = claude.GoalClear(context.Background(), session)
			} else {
				err = claude.GoalSet(context.Background(), session, test.objective)
			}
			if !test.check(err) || remote.postCount() != 1 || postedText(remote.posts[0]) != "/goal "+test.args {
				t.Fatalf("err=%v posts=%v", err, remote.posts)
			}
		})
	}
}

func TestClaudeGoalCommandWaitsForAnIdleSessionAndItsAnswer(t *testing.T) {
	shortControlPoll(t)
	fixture, session := idleGoalSession(t)
	remote := newFakeRemoteControl(t)
	claude := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, remote.request)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := claude.GoalSet(ctx, session, "tests pass"); !surface.IsDeliveryOutcomeUnknown(err) || remote.postCount() != 1 {
		t.Fatalf("unanswered err=%v posts=%d", err, remote.postCount())
	}

	fixture.RecordStatus("conversation-test", fixture.Home, "session_test", "busy", "", time.Time{})
	appendTranscript(t, session.Transcript, `{"type":"user","uuid":"u2","message":{"content":"working"}}`+"\n")
	if err := claude.GoalSet(context.Background(), session, "tests pass"); !surface.IsDeliveryUnavailable(err) || remote.postCount() != 1 {
		t.Fatalf("busy err=%v posts=%d", err, remote.postCount())
	}

	peer := &surface.Session{ID: "conversation-test", Surface: surface.KindClaude, Transport: "uds", PID: fixture.PID, Transcript: session.Transcript}
	if err := claude.GoalClear(context.Background(), peer); !errors.Is(err, errClaudeGoalPeer) || remote.postCount() != 1 {
		t.Fatalf("peer err=%v posts=%d", err, remote.postCount())
	}
}
