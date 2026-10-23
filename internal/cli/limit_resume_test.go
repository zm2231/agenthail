package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestLimitResumeListsStopsAndTurnsTheSettingOff(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app, r := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	session := surface.Session{ID: "limited", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	resetsAt := time.Date(2026, 10, 23, 15, 0, 0, 0, time.UTC)
	if err := r.RecordLimitStop(session, "/roots/work", surface.LimitStop{TurnID: "u1", RefusedAt: resetsAt.Add(-time.Hour), ResetsAt: resetsAt}); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"limit-resume", "--json"}) })
	var rows []struct {
		SessionID string                    `json:"sessionId"`
		State     registry.LimitResumeState `json:"state"`
		ResetsAt  time.Time                 `json:"resetsAt"`
	}
	if err != nil || json.Unmarshal([]byte(output), &rows) != nil || len(rows) != 1 || rows[0].SessionID != "limited" || rows[0].State != registry.LimitResumeWaiting || !rows[0].ResetsAt.Equal(resetsAt) {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"limit-resume", "off"}) }); err != nil {
		t.Fatal(err)
	}
	if config, err := daemon.LoadDashboardConfig(); err != nil || config.ResumesAfterLimit() {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	if _, err := captureStdout(t, func() error { return app.Run([]string{"limit-resume", "on"}) }); err != nil {
		t.Fatal(err)
	}
	if config, err := daemon.LoadDashboardConfig(); err != nil || !config.ResumesAfterLimit() {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	if err := app.Run([]string{"limit-resume", "sideways"}); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
		t.Fatalf("err=%v", err)
	}
}
