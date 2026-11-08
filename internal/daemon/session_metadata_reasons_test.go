package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/surface"
)

func metadataErrorsFor(t *testing.T, runsErr error, runs []surface.ClaudeRunObservation, processStart time.Time) (map[string]string, []surface.ClaudeRunObservation) {
	t.Helper()
	_, registry, _, from, _ := daemonFixture(t)
	from.Surface, from.RuntimeKind, from.ID = surface.KindClaude, surface.RuntimeClaudeCLI, "claude-session"
	if err := registry.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	fake := &dashboardClaudeRunSurface{
		daemonSurface: &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{from.ID: from}, accepted: true},
		runs:          runs,
		runsErr:       runsErr,
		processStart:  processStart,
	}
	response := serveDashboardRequest(dashboardRouter(newTestDaemon(t, registry, []surface.Surface{fake})), http.MethodGet, "/api/session-metadata?id="+from.ID, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Errors map[string]string              `json:"errors"`
		Runs   []surface.ClaudeRunObservation `json:"claudeRuns"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Errors, body.Runs
}

func TestAMetadataReadThatFailsSaysWhy(t *testing.T) {
	long := strings.Repeat("x", 400)
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"timeout", fmt.Errorf("observe runs: %w", context.DeadlineExceeded), "Timed out after 3s."},
		{"unsupported", fmt.Errorf("%w: no job records here", surface.ErrUnsupported), "Not supported for this session."},
		{"read error", errors.New("parse Claude job record /jobs/1/state.json: unexpected end of JSON input\nsecond line"), "Could not read: parse Claude job record /jobs/1/state.json: unexpected end of JSON input"},
	} {
		got, _ := metadataErrorsFor(t, tc.err, nil, time.Time{})
		if got["claudeRuns"] != tc.want {
			t.Errorf("%s: errors=%q, want claudeRuns %q", tc.name, got, tc.want)
		}
	}
	got, _ := metadataErrorsFor(t, errors.New(long), nil, time.Time{})
	reason := got["claudeRuns"]
	if !strings.HasPrefix(reason, "Could not read: xxx") || !strings.HasSuffix(reason, "…") || utf8.RuneCountInString(strings.TrimPrefix(reason, "Could not read: ")) != metadataReasonLimit {
		t.Errorf("a long error is not bounded to %d characters: %q", metadataReasonLimit, reason)
	}
}

func TestALiveSessionsMetadataLeavesOutRunsFromBeforeItsProcess(t *testing.T) {
	start := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	runs := []surface.ClaudeRunObservation{
		{JobID: "july", SessionID: "claude-session", RunType: "bg", ProviderState: "failed", UpdatedAt: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)},
		{JobID: "now-a", SessionID: "claude-session", RunType: "bg", ProviderState: "working", UpdatedAt: start.Add(time.Minute)},
		{JobID: "now-b", ResumeSessionID: "claude-session", RunType: "bg", ProviderState: "working", UpdatedAt: start.Add(2 * time.Minute)},
	}
	errs, got := metadataErrorsFor(t, nil, runs, start)
	if len(errs) != 0 || len(got) != 2 || got[0].JobID != "now-a" || got[1].JobID != "now-b" {
		t.Fatalf("runs=%+v errors=%v, want both runs of the current process and not the July one", got, errs)
	}
}
