package surfaces

import (
	"context"
	"os"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexSessionSettingsComeFromTheRolloutWithTheSandboxOfTheLastTurn(t *testing.T) {
	_, codex, session := settingsThread(t, settingsModels)
	if err := os.WriteFile(session.Transcript, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	appendLine(t, session.Transcript, `{"type":"turn_context","payload":{"model":"gpt-luna","approval_policy":"on-request","sandbox_policy":{"type":"workspace-write"},"collaboration_mode":{"mode":"plan"}}}`)
	appendLine(t, session.Transcript, `{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"gpt-sol","approval_policy":"never","reasoning_effort":null,"collaboration_mode":{"mode":"default"}}}}`)

	settings, err := codex.SessionSettings(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if m := settings.Model; m == nil || m.Value != "gpt-sol" || m.Source != surface.SettingFromRollout || m.Listed == nil || !*m.Listed {
		t.Fatalf("model = %+v", m)
	}
	if e := settings.Effort; e == nil || e.Value != "medium" || e.Source != surface.SettingFromRollout {
		t.Fatalf("an unset effort is the model's default: %+v", e)
	}
	if p := settings.Permission; p == nil || p.ApprovalPolicy != "never" || p.Sandbox != "workspace-write" || p.Mode != "" {
		t.Fatalf("permission = %+v", p)
	}
	if c := settings.CollaborationMode; c == nil || c.Value != "default" {
		t.Fatalf("collaboration mode = %+v", c)
	}

	appendLine(t, session.Transcript, `{"type":"turn_context","payload":{"model":"gpt-private","effort":"high","approval_policy":"untrusted","sandbox_policy":{"type":"read-only"}}}`)
	settings, err = codex.SessionSettings(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if m := settings.Model; m == nil || m.Value != "gpt-private" || m.Listed == nil || *m.Listed {
		t.Fatalf("a model the runtime does not list is kept and marked unlisted: %+v", m)
	}
	if p := settings.Permission; p == nil || p.ApprovalPolicy != "untrusted" || p.Sandbox != "read-only" {
		t.Fatalf("permission after a later turn = %+v", p)
	}
}

func TestCodexSessionSettingsWithoutARolloutAreUnavailable(t *testing.T) {
	_, codex, session := settingsThread(t, settingsModels)
	session.Transcript = ""
	settings, err := codex.SessionSettings(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Model != nil || settings.Effort != nil || settings.Permission != nil {
		t.Fatalf("settings were guessed: %+v", settings)
	}
	for _, field := range []string{"model", "effort", "permission"} {
		if settings.Unavailable[field] == "" {
			t.Fatalf("unavailable = %v, want a reason for %s", settings.Unavailable, field)
		}
	}
}
