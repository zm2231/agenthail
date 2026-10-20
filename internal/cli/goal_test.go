package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestGoalReportsStatusUsageAndAttention(t *testing.T) {
	budget := int64(1000)
	at := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	fake := &cliSurface{
		kind:     surface.KindCodex,
		sessions: map[string]surface.Session{"g": {ID: "g", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}},
		caps:     surface.Capabilities{Goal: true, GoalStatus: true},
	}
	app, _ := cliFixture(t, fake)
	show := func(goal *surface.GoalState, flags ...string) string {
		t.Helper()
		fake.goal = goal
		output, err := captureStdout(t, func() error { return app.Run(append([]string{"goal", "codex:g"}, flags...)) })
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	full := func(status string) *surface.GoalState {
		return &surface.GoalState{Objective: "Ship", Status: status, TimeUsedSeconds: 72, TokensUsed: 480, TokenBudget: &budget, CreatedAt: at, UpdatedAt: at.Add(time.Minute)}
	}
	baseline := strings.Count(show(full(surface.GoalStatusActive)), "\n")
	for _, status := range []string{surface.GoalStatusActive, surface.GoalStatusPaused, surface.GoalStatusBlocked, surface.GoalStatusUsageLimited, surface.GoalStatusBudgetLimited, surface.GoalStatusComplete} {
		output := show(full(status))
		for _, value := range []string{"Ship", status, "72", "480", "1000", "2026-10-04T08:00:00Z", "2026-10-04T08:01:00Z"} {
			if !strings.Contains(output, value) {
				t.Fatalf("status=%s missing %q in %s", status, value, output)
			}
		}
		needsAttention := status == surface.GoalStatusBlocked || status == surface.GoalStatusUsageLimited || status == surface.GoalStatusBudgetLimited
		if flagged := strings.Count(output, "\n") > baseline; flagged != needsAttention {
			t.Fatalf("status=%s attention flagged=%v output=%s", status, flagged, output)
		}
	}
	sparse := show(&surface.GoalState{Objective: "Ship", Status: surface.GoalStatusActive})
	if strings.Contains(sparse, "1000") || strings.Contains(sparse, "2026-") {
		t.Fatalf("absent budget or timestamps invented: %s", sparse)
	}
	var document struct {
		Session string            `json:"session"`
		Goal    surface.GoalState `json:"goal"`
	}
	if err := json.Unmarshal([]byte(show(full(surface.GoalStatusBlocked), "--json")), &document); err != nil {
		t.Fatal(err)
	}
	if document.Session != "g" || document.Goal.Status != surface.GoalStatusBlocked || document.Goal.TokenBudget == nil || *document.Goal.TokenBudget != budget || !document.Goal.CreatedAt.Equal(at) {
		t.Fatalf("document=%+v", document)
	}
}

func TestGoalShowsAReportedGoalButRefusesControlsTheSessionLacks(t *testing.T) {
	fake := &cliSurface{
		kind:     surface.KindClaude,
		sessions: map[string]surface.Session{"g": {ID: "g", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}},
		caps:     surface.Capabilities{GoalStatus: true},
		goal:     &surface.GoalState{Objective: "tests pass", Status: surface.GoalStatusBlocked, Reason: "the test binary cannot be built here"},
	}
	app, _ := cliFixture(t, fake)
	output, err := captureStdout(t, func() error { return app.Run([]string{"goal", "claude:g"}) })
	if err != nil || !strings.Contains(output, "tests pass [blocked]") || !strings.Contains(output, "Needs you") || !strings.Contains(output, "Reason: the test binary cannot be built here") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	for _, action := range [][]string{{"set", "lint"}, {"clear"}} {
		if err := app.Run(append([]string{"goal", "claude:g"}, action...)); err == nil || !strings.Contains(err.Error(), "cannot set or clear this session's goal") {
			t.Fatalf("%v err=%v", action, err)
		}
	}
	for _, action := range [][]string{{"pause"}, {"resume"}, {"budget", "10"}} {
		if err := app.Run(append([]string{"goal", "claude:g"}, action...)); err == nil || !strings.Contains(err.Error(), "does not support goal "+action[0]) {
			t.Fatalf("%v err=%v", action, err)
		}
	}
}

type goalCommandCLISurface struct {
	*cliSurface
	ran []string
}

func (s *goalCommandCLISurface) GoalCommand(_ *surface.Session, objective string) (string, error) {
	if objective == "" {
		return "/goal clear", nil
	}
	return "/goal " + objective, nil
}

func (s *goalCommandCLISurface) RunGoalCommand(_ context.Context, _ *surface.Session, command string) error {
	s.ran = append(s.ran, command)
	return nil
}

func TestGoalSetAndClearAreHeldForASessionThatRunsGoalCommands(t *testing.T) {
	fake := &goalCommandCLISurface{cliSurface: &cliSurface{
		kind:     surface.KindClaude,
		sessions: map[string]surface.Session{"g": {ID: "g", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}},
		caps:     surface.Capabilities{GoalSet: true, GoalStatus: true},
	}}
	app, registry := cliFixture(t, fake.cliSurface)
	app.Surfaces = []SurfaceEntry{{Name: string(surface.KindClaude), Surface: fake}}
	for _, action := range [][]string{{"set", "tests", "pass"}, {"edit", "lint", "is", "clean"}, {"clear"}} {
		output, err := captureStdout(t, func() error { return app.Run(append([]string{"goal", "claude:g"}, action...)) })
		if err != nil || !strings.Contains(output, "queued for") || !strings.Contains(output, "will run when the target is idle") {
			t.Fatalf("%v output=%q err=%v", action, output, err)
		}
	}
	items, err := registry.ListQueue(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, item := range items {
		commands = append(commands, item.Message)
	}
	if strings.Join(commands, "|") != "/goal tests pass|/goal lint is clean|/goal clear" || len(fake.ran) != 0 {
		t.Fatalf("queued=%v ran=%v", commands, fake.ran)
	}
	if err := app.Run([]string{"goal", "claude:g", "pause"}); err == nil {
		t.Fatal("pause ran on a session without goal controls")
	}
}
