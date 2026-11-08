package surfaces

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAHostedClaudeSessionReportsItsSettingsFromTheRunningProcess(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	ctx := context.Background()
	for _, change := range [][2]string{{surface.TurnSettingEffort, "xhigh"}, {surface.TurnSettingMode, "plan"}, {surface.TurnSettingModel, "opus"}} {
		if err := c.SetTurnSetting(ctx, sess, change[0], change[1]); err != nil {
			t.Fatalf("set %s: %v", change[0], err)
		}
	}

	settings, err := c.SessionSettings(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if m := settings.Model; m == nil || m.Value != "opus" || m.Source != surface.SettingFromRuntime {
		t.Fatalf("model = %+v", m)
	}
	if e := settings.Effort; e == nil || e.Value != "xhigh" || e.Source != surface.SettingFromRuntime {
		t.Fatalf("effort = %+v", e)
	}
	if p := settings.Permission; p == nil || p.Mode != "plan" || p.Source != surface.SettingFromRuntime {
		t.Fatalf("permission = %+v", p)
	}
}

func TestAStoppedClaudeSessionReportsItsSettingsFromItsTranscriptWithoutStarting(t *testing.T) {
	fixture, host, c := hostResume(t)
	session := exitedSession(fixture)
	appendLine(t, session.Transcript, `{"type":"user","permissionMode":"acceptEdits","message":{"role":"user","content":"go"}}`)
	appendLine(t, session.Transcript, `{"type":"assistant","message":{"model":"claude-opus-5-5","role":"assistant","content":[]}}`)
	appendLine(t, session.Transcript, `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`)

	settings, err := c.SessionSettings(context.Background(), &session)
	if err != nil {
		t.Fatal(err)
	}
	if m := settings.Model; m == nil || m.Value != "claude-opus-5-5" || m.Source != surface.SettingFromTranscript {
		t.Fatalf("model = %+v", m)
	}
	if p := settings.Permission; p == nil || p.Mode != "acceptEdits" || p.Source != surface.SettingFromTranscript {
		t.Fatalf("permission = %+v", p)
	}
	if settings.Effort != nil || settings.Unavailable["effort"] == "" {
		t.Fatalf("effort = %+v unavailable=%v, want it unknown with a reason", settings.Effort, settings.Unavailable)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("reading settings started the conversation: %+v", spawns)
	}
}
