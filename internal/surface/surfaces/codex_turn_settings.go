package surfaces

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// CodexThreadSettingsVersion is the first Codex whose app-server has thread/settings/update.
const CodexThreadSettingsVersion = "0.133.0"

type codexThreadSettings struct {
	Model    string
	Effort   string
	Mode     string
	Approval string
	Sandbox  string
}

var codexModeOptions = []surface.TurnSettingOption{{ID: "default", Label: "Default"}, {ID: "plan", Label: "Plan"}}

func (c *Codex) TurnSettings(ctx context.Context, sess *surface.Session) ([]surface.TurnSetting, error) {
	current, _ := readCodexThreadSettings(ctx, sess.Transcript)
	if current.Model == "" {
		model, err := c.Model(ctx, sess, "")
		if err != nil {
			return nil, err
		}
		current.Model = model
	}
	models, _ := c.Models(ctx)
	return codexTurnSettings(current, models), nil
}

func codexTurnSettings(current codexThreadSettings, models []surface.ModelOption) []surface.TurnSetting {
	model := surface.TurnSetting{Key: surface.TurnSettingModel, Value: current.Model, Applies: surface.TurnSettingAppliesNextTurn}
	var running *surface.ModelOption
	for i, option := range models {
		model.Options = append(model.Options, surface.TurnSettingOption{ID: option.ID, Label: option.DisplayName})
		if option.ID == current.Model {
			running = &models[i]
		}
	}
	if running == nil && current.Model != "" {
		model.Options = append(model.Options, surface.TurnSettingOption{ID: current.Model, Label: current.Model})
	}
	settings := []surface.TurnSetting{model}
	if running != nil && len(running.SupportedReasoningEfforts) > 0 {
		effort := surface.TurnSetting{Key: surface.TurnSettingEffort, Value: current.Effort, Applies: surface.TurnSettingAppliesNextTurn}
		if effort.Value == "" {
			effort.Value = running.DefaultReasoningEffort
		}
		for _, level := range running.SupportedReasoningEfforts {
			effort.Options = append(effort.Options, surface.TurnSettingOption{ID: level, Label: effortLabel(level)})
		}
		settings = append(settings, effort)
	}
	mode := surface.TurnSetting{Key: surface.TurnSettingMode, Value: current.Mode, Options: codexModeOptions, Applies: surface.TurnSettingAppliesNextTurn}
	if mode.Value == "" {
		mode.Value = "default"
	}
	return append(settings, mode)
}

func effortLabel(level string) string {
	if level == "xhigh" {
		return "Extra high"
	}
	return strings.ToUpper(level[:1]) + level[1:]
}

func (c *Codex) SetTurnSetting(ctx context.Context, sess *surface.Session, key, value string) error {
	if key == surface.TurnSettingModel {
		if err := surface.ValidModelName(value); err != nil {
			return err
		}
	}
	release, err := acquireCodexThreadWrite(ctx, sess.ID)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer release()
	client, err := c.openRoute(ctx, sess)
	if err != nil {
		return surface.DeliveryUnavailable(err)
	}
	defer client.Close()
	if err := c.requireDirectInput(ctx, client, sess); err != nil {
		if surface.IsDeliveryTerminal(err) {
			return err
		}
		return surface.DeliveryUnavailable(err)
	}
	var settings []surface.TurnSetting
	if key != surface.TurnSettingModel {
		if settings, err = c.TurnSettings(ctx, sess); err != nil {
			return err
		}
		if err := surface.ValidTurnSetting(settings, key, value); err != nil {
			return err
		}
	}
	params := map[string]any{"threadId": sess.ID}
	switch key {
	case surface.TurnSettingModel:
		params["model"] = value
	case surface.TurnSettingEffort:
		params["effort"] = value
	case surface.TurnSettingMode:
		var model, effort any
		for _, setting := range settings {
			switch {
			case setting.Key == surface.TurnSettingModel:
				model = setting.Value
			case setting.Key == surface.TurnSettingEffort && setting.Value != "":
				effort = setting.Value
			}
		}
		params["collaborationMode"] = map[string]any{"mode": value, "settings": map[string]any{"model": model, "reasoning_effort": effort, "developer_instructions": nil}}
	}
	if _, err := client.Request(ctx, "thread/settings/update", params, 10*time.Second); err != nil {
		return codexThreadSettingsError(err)
	}
	return nil
}

func codexThreadSettingsError(err error) error {
	var rpc *codexRPCError
	if errors.As(err, &rpc) && strings.Contains(rpc.Message, "unknown variant `thread/settings/update`") {
		return &surface.RuntimeVersionError{Runtime: "Codex", Feature: "Changing a thread's model, effort or mode", Minimum: CodexThreadSettingsVersion}
	}
	return classifyCodexWrite(err)
}

// readCodexThreadSettings reads the settings the newest turn ran with, or a
// later change Codex recorded as thread_settings_applied.
func readCodexThreadSettings(ctx context.Context, rollout string) (codexThreadSettings, bool) {
	return readNewestCodexSettings(ctx, rollout, func(line []byte) bool {
		return bytes.Contains(line, []byte(`"turn_context"`)) || bytes.Contains(line, []byte(`"thread_settings_applied"`))
	}, codexSettingsRecord)
}

// readCodexTurnContext reads the newest turn's own record, which alone
// carries the sandbox a turn ran in.
func readCodexTurnContext(ctx context.Context, rollout string) (codexThreadSettings, bool) {
	return readNewestCodexSettings(ctx, rollout, func(line []byte) bool {
		return bytes.Contains(line, []byte(`"turn_context"`))
	}, func(record map[string]any) (codexThreadSettings, bool) {
		if str(record, "type") != "turn_context" {
			return codexThreadSettings{}, false
		}
		return codexSettingsRecord(record)
	})
}

func readNewestCodexSettings(ctx context.Context, rollout string, candidate func([]byte) bool, read func(map[string]any) (codexThreadSettings, bool)) (codexThreadSettings, bool) {
	if rollout == "" {
		return codexThreadSettings{}, false
	}
	path, err := codexReadableRollout(rollout)
	if err != nil {
		return codexThreadSettings{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return codexThreadSettings{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return codexThreadSettings{}, false
	}
	match := func(record map[string]any) bool { _, ok := read(record); return ok }
	start, found, err := transcriptRecordBefore(ctx, file, info.Size(), candidate, match)
	if err != nil || !found {
		return codexThreadSettings{}, false
	}
	var settings codexThreadSettings
	var ok bool
	_ = scanTranscriptRecords(ctx, file, start, info.Size(), true, func(record map[string]any, _ int64, _ []byte) {
		if !ok {
			settings, ok = read(record)
		}
	})
	return settings, ok
}

func codexSettingsRecord(record map[string]any) (codexThreadSettings, bool) {
	payload, _ := record["payload"].(map[string]any)
	switch {
	case str(record, "type") == "turn_context":
		mode, _ := payload["collaboration_mode"].(map[string]any)
		sandbox, _ := payload["sandbox_policy"].(map[string]any)
		return codexThreadSettings{Model: str(payload, "model"), Effort: str(payload, "effort"), Mode: str(mode, "mode"), Approval: str(payload, "approval_policy"), Sandbox: str(sandbox, "type")}, true
	case str(record, "type") == "event_msg" && str(payload, "type") == "thread_settings_applied":
		applied, _ := payload["thread_settings"].(map[string]any)
		mode, _ := applied["collaboration_mode"].(map[string]any)
		return codexThreadSettings{Model: str(applied, "model"), Effort: str(applied, "reasoning_effort"), Mode: str(mode, "mode"), Approval: str(applied, "approval_policy")}, true
	}
	return codexThreadSettings{}, false
}
