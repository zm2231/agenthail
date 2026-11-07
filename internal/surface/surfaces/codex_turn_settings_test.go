package surfaces

import (
	"context"
	"errors"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
)

const settingsThreadID = "019f004a-0000-7000-8000-000000000042"

func settingsModels(method string, _ map[string]any) map[string]any {
	if method != "model/list" {
		return nil
	}
	return map[string]any{"data": []any{
		map[string]any{"model": "gpt-luna", "displayName": "Luna", "supportedReasoningEfforts": []any{"low", "medium", "high"}, "defaultReasoningEffort": "medium"},
		map[string]any{"model": "gpt-sol", "displayName": "Sol", "supportedReasoningEfforts": []any{"medium", "xhigh"}, "defaultReasoningEffort": "medium"},
	}}
}

func settingsThread(t *testing.T, respond codextest.Responder) (*codextest.Daemon, *Codex, *surface.Session) {
	t.Helper()
	fake := startManagedCodex(t, respond)
	fake.AddThread(codextest.Thread{ID: settingsThreadID, Originator: "agenthail", Model: "gpt-luna"})
	fake.Load(settingsThreadID)
	session := managedSession()
	session.ID = settingsThreadID
	session.Transcript = fake.RolloutPath(settingsThreadID)
	return fake, isolatedManagedRuntime(t), session
}

func settingValue(t *testing.T, settings []surface.TurnSetting, key string) surface.TurnSetting {
	t.Helper()
	for _, setting := range settings {
		if setting.Key == key {
			return setting
		}
	}
	t.Fatalf("no %s setting in %+v", key, settings)
	return surface.TurnSetting{}
}

func lastTurn(t *testing.T, fake *codextest.Daemon) (codextest.Turn, map[string]any) {
	t.Helper()
	starts := fake.Calls("turn/start")
	thread, _ := fake.Thread(settingsThreadID)
	if len(starts) == 0 || len(thread.Turns) == 0 {
		t.Fatalf("no turn started; methods=%v", fake.Methods())
	}
	return thread.Turns[len(thread.Turns)-1], starts[len(starts)-1]
}

func TestCodexTurnSettingsReachTheNextTurnsWithoutTurnOverrides(t *testing.T) {
	fake, codex, session := settingsThread(t, settingsModels)
	ctx := context.Background()
	for _, change := range [][2]string{{surface.TurnSettingModel, "gpt-sol"}, {surface.TurnSettingEffort, "xhigh"}, {surface.TurnSettingMode, "plan"}} {
		if err := codex.SetTurnSetting(ctx, session, change[0], change[1]); err != nil {
			t.Fatalf("set %s: %v", change[0], err)
		}
	}
	if calls := fake.Calls("thread/settings/update"); len(calls) != 3 {
		t.Fatalf("thread/settings/update calls=%v", calls)
	}
	mode := fake.Calls("thread/settings/update")[2]["collaborationMode"].(map[string]any)
	if settings := mode["settings"].(map[string]any); settings["model"] != "gpt-sol" || settings["reasoning_effort"] != "xhigh" {
		t.Fatalf("the mode change carried settings %v; Codex takes them over the thread's", settings)
	}
	settings, err := codex.TurnSettings(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if got := [3]string{settingValue(t, settings, "model").Value, settingValue(t, settings, "effort").Value, settingValue(t, settings, "mode").Value}; got != [3]string{"gpt-sol", "xhigh", "plan"} {
		t.Fatalf("settings read back as %v", got)
	}
	if options := settingValue(t, settings, "effort").Options; len(options) != 2 || options[1].ID != "xhigh" || options[1].Label != "Extra high" {
		t.Fatalf("effort options follow the running model: %+v", options)
	}
	for _, message := range []string{"first", "second"} {
		if _, err := codex.SendWithOptions(ctx, session, message, surface.SendOptions{}); err != nil {
			t.Fatal(err)
		}
		turn, params := lastTurn(t, fake)
		if params["model"] != nil || params["effort"] != nil || params["collaborationMode"] != nil {
			t.Fatalf("turn/start carried overrides %v", params)
		}
		if turn.Model != "gpt-sol" || turn.Effort != "xhigh" || turn.Mode != "plan" {
			t.Fatalf("turn %q ran on %+v", message, turn)
		}
		fake.CompleteTurn(settingsThreadID, "done")
	}
}

func TestCodexTurnSettingsKeepAnyModelNameButOnlyOfferedEffortsAndModes(t *testing.T) {
	fake, codex, session := settingsThread(t, settingsModels)
	ctx := context.Background()
	if err := codex.SetTurnSetting(ctx, session, surface.TurnSettingModel, "gpt-hidden"); err != nil {
		t.Fatalf("a model the catalog hides: %v", err)
	}
	for _, bad := range [][2]string{{surface.TurnSettingModel, "two words"}, {surface.TurnSettingEffort, "ultra"}, {surface.TurnSettingMode, "auto"}} {
		if err := codex.SetTurnSetting(ctx, session, bad[0], bad[1]); err == nil {
			t.Fatalf("%s=%q was accepted", bad[0], bad[1])
		}
	}
	if calls := fake.Calls("thread/settings/update"); len(calls) != 1 {
		t.Fatalf("rejected values reached Codex: %v", calls)
	}
	settings, err := codex.TurnSettings(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	model := settingValue(t, settings, "model")
	if model.Value != "gpt-hidden" || model.Options[len(model.Options)-1].ID != "gpt-hidden" {
		t.Fatalf("the running model is missing from its options: %+v", model)
	}
	for _, setting := range settings {
		if setting.Key == surface.TurnSettingEffort {
			t.Fatalf("an effort control for a model whose efforts are unknown: %+v", setting)
		}
	}
}

func TestCodexTurnSettingsOnAnAppServerWithoutTheMethodNameTheVersion(t *testing.T) {
	old := func(method string, params map[string]any) map[string]any {
		if method == "thread/settings/update" {
			return map[string]any{"error": map[string]any{"code": -32600, "message": "Invalid request: unknown variant `thread/settings/update`, expected one of `initialize`, `thread/start`"}}
		}
		return settingsModels(method, params)
	}
	_, codex, session := settingsThread(t, old)
	err := codex.SetTurnSetting(context.Background(), session, surface.TurnSettingEffort, "high")
	version, ok := surface.IsRuntimeVersionError(err)
	if !ok || version.Minimum != CodexThreadSettingsVersion || version.Runtime != "Codex" || !errors.Is(err, surface.ErrUnsupported) {
		t.Fatalf("err=%v", err)
	}
	if _, err := codex.SetModel(context.Background(), session, "gpt-sol"); !errors.Is(err, surface.ErrUnsupported) {
		t.Fatalf("SetModel on an old app-server: %v", err)
	}
}

func TestCodexTurnSettingsLostToAnotherWriterAreAnOwnershipConflict(t *testing.T) {
	taken := func(method string, params map[string]any) map[string]any {
		if method == "thread/settings/update" {
			return map[string]any{"error": map[string]any{"code": -32600, "message": "thread " + settingsThreadID + " already has an active writer"}}
		}
		return settingsModels(method, params)
	}
	_, codex, session := settingsThread(t, taken)
	requireOwnershipConflict(t, codex.SetTurnSetting(context.Background(), session, surface.TurnSettingEffort, "high"))
}

func TestCodexThreadSettingsComeFromTheNewestRecord(t *testing.T) {
	path := t.TempDir() + "/rollout.jsonl"
	appendLine(t, path, `{"type":"turn_context","payload":{"model":"gpt-luna","effort":"low","collaboration_mode":{"mode":"plan"}}}`)
	appendLine(t, path, `{"type":"event_msg","payload":{"type":"thread_settings_applied","thread_settings":{"model":"gpt-sol","reasoning_effort":"high","collaboration_mode":{"mode":"default"}}}}`)
	if got, ok := readCodexThreadSettings(context.Background(), path); !ok || got != (codexThreadSettings{Model: "gpt-sol", Effort: "high", Mode: "default"}) {
		t.Fatalf("after a settings change: %+v ok=%v", got, ok)
	}
	appendLine(t, path, `{"type":"turn_context","payload":{"model":"gpt-sol","effort":"medium"}}`)
	if got, ok := readCodexThreadSettings(context.Background(), path); !ok || got != (codexThreadSettings{Model: "gpt-sol", Effort: "medium"}) {
		t.Fatalf("after a later turn: %+v ok=%v", got, ok)
	}
}
