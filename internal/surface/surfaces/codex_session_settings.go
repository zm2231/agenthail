package surfaces

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) SessionSettings(ctx context.Context, sess *surface.Session) (surface.RuntimeSettings, error) {
	var settings surface.RuntimeSettings
	current, ok := readCodexThreadSettings(ctx, sess.Transcript)
	if !ok {
		why := "the thread's rollout has no turn yet"
		if sess.Transcript == "" {
			why = "the thread has no rollout on this machine"
		}
		for _, field := range []string{"model", "effort", "permission", "collaborationMode"} {
			settings.Missing(field, why)
		}
		return settings, nil
	}
	models, _ := c.Models(ctx)
	settings.Model = modelSetting(current.Model, surface.SettingFromRollout, models)
	if settings.Model == nil {
		settings.Missing("model", "the rollout names no model")
	}
	settings.Effort = codexEffortSetting(current, models)
	if settings.Effort == nil {
		settings.Missing("effort", "the rollout names no effort and the model's default is unknown")
	}
	permission := surface.PermissionSetting{ApprovalPolicy: current.Approval, Source: surface.SettingFromRollout}
	if turn, ok := readCodexTurnContext(ctx, sess.Transcript); ok {
		permission.Sandbox = turn.Sandbox
	}
	if permission.ApprovalPolicy != "" || permission.Sandbox != "" {
		settings.Permission = &permission
	} else {
		settings.Missing("permission", "the rollout names no approval policy or sandbox")
	}
	if current.Mode != "" {
		settings.CollaborationMode = &surface.SettingValue{Value: current.Mode, Source: surface.SettingFromRollout}
	} else {
		settings.Missing("collaborationMode", "the rollout names no collaboration mode")
	}
	return settings, nil
}

func codexEffortSetting(current codexThreadSettings, models []surface.ModelOption) *surface.SettingValue {
	if current.Effort != "" {
		return &surface.SettingValue{Value: current.Effort, Source: surface.SettingFromRollout}
	}
	for _, option := range models {
		if option.ID == current.Model && option.DefaultReasoningEffort != "" {
			return &surface.SettingValue{Value: option.DefaultReasoningEffort, Source: surface.SettingFromRollout}
		}
	}
	return nil
}
