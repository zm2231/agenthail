package surfaces

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
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

func TestClaudeStreamSendsTheGoalWhenClaudeRecordsAVerdict(t *testing.T) {
	path := writeTranscript(t, `{"type":"user","uuid":"u1","message":{"content":"/goal tests pass"}}`+"\n"+goalSetRecord)
	session := seededClaudeStreamSession(t, path, "")
	go func() {
		time.Sleep(50 * time.Millisecond)
		appendTestTranscript(t, path, `{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}}`, goalMetRecord)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var goals []*surface.GoalState
	err := NewClaude(nil, t.TempDir()).Stream(ctx, session, "", func(event surface.StreamEvent) {
		if event.Kind == "goal" {
			goals = append(goals, event.Goal)
			cancel()
		}
	}, time.Second)
	if err != context.Canceled || len(goals) != 1 || goals[0] == nil || goals[0].Status != surface.GoalStatusComplete || goals[0].Reason != "go test exited 0" {
		t.Fatalf("err=%v goals=%+v", err, goals)
	}
}

func TestClaudeReportsGoalStatusWithoutGoalControls(t *testing.T) {
	capabilities := NewClaude(nil, t.TempDir()).Capabilities()
	if !capabilities.GoalStatus || capabilities.Goal {
		t.Fatalf("capabilities=%+v", capabilities)
	}
}
