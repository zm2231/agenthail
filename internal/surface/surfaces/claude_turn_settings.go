package surfaces

import (
	"context"

	"github.com/zm2231/agenthail/internal/surface"
)

var claudeModeOptions = []surface.TurnSettingOption{
	{ID: "manual", Label: "Manual"},
	{ID: "acceptEdits", Label: "Accept edits"},
	{ID: "plan", Label: "Plan"},
	{ID: "auto", Label: "Auto"},
	{ID: "dontAsk", Label: "Don't ask", Confirm: true},
	{ID: "bypassPermissions", Label: "Bypass permissions", Confirm: true},
}

var (
	claudeRemoteEfforts = []string{"low", "medium", "high", "max"}
	claudeHostEfforts   = []string{"low", "medium", "high", "xhigh", "max"}
)

func (c *Claude) TurnSettings(ctx context.Context, sess *surface.Session) ([]surface.TurnSetting, error) {
	hosted := claudeHosted(sess)
	if !hosted && !surface.EffectiveCapabilities(c, sess).Commands {
		return nil, nil
	}
	models, _ := c.Models(ctx)
	var model, effort, mode string
	var err error
	if hosted {
		listed, ok := c.host.session(ctx, sess.ID, sess.PID)
		if !ok {
			return nil, nil
		}
		model, effort, mode = listed.Model, listed.Effort, listed.PermissionMode
	} else if model, err = c.Model(ctx, sess, ""); err != nil {
		return nil, err
	}
	return claudeTurnSettings(models, model, effort, mode, hosted), nil
}

func claudeTurnSettings(models []surface.ModelOption, model, effort, mode string, hosted bool) []surface.TurnSetting {
	if model == "" {
		model = "default"
	}
	modelSetting := surface.TurnSetting{Key: surface.TurnSettingModel, Value: model, Applies: surface.TurnSettingAppliesNow}
	efforts := claudeRemoteEfforts
	if hosted {
		efforts = claudeHostEfforts
	}
	known := false
	for _, option := range models {
		modelSetting.Options = append(modelSetting.Options, surface.TurnSettingOption{ID: option.ID, Label: option.DisplayName})
		if option.ID == model {
			known = true
			if hosted && len(option.SupportedReasoningEfforts) > 0 {
				efforts = option.SupportedReasoningEfforts
			}
		}
	}
	if !known {
		modelSetting.Options = append(modelSetting.Options, surface.TurnSettingOption{ID: model, Label: model})
	}
	effortSetting := surface.TurnSetting{Key: surface.TurnSettingEffort, Value: effort, Applies: surface.TurnSettingAppliesNow}
	for _, level := range efforts {
		effortSetting.Options = append(effortSetting.Options, surface.TurnSettingOption{ID: level, Label: effortLabel(level)})
	}
	settings := []surface.TurnSetting{modelSetting, effortSetting}
	if hosted {
		settings = append(settings, surface.TurnSetting{Key: surface.TurnSettingMode, Value: mode, Options: claudeModeOptions, Applies: surface.TurnSettingAppliesNow})
	}
	return settings
}

func (c *Claude) SetTurnSetting(ctx context.Context, sess *surface.Session, key, value string) error {
	if key == surface.TurnSettingModel {
		if err := surface.ValidModelName(value); err != nil {
			return err
		}
	} else {
		settings, err := c.TurnSettings(ctx, sess)
		if err != nil {
			return err
		}
		if err := surface.ValidTurnSetting(settings, key, value); err != nil {
			return err
		}
	}
	action := key
	if key == surface.TurnSettingMode {
		action = "permission-mode"
	}
	if key == surface.TurnSettingEffort && claudeHosted(sess) {
		if err := c.EnsureWritable(ctx, sess); err != nil {
			return err
		}
		_, err := c.hostSetting(ctx, sess, action, value)
		return err
	}
	_, err := c.Control(ctx, sess, surface.ControlRequest{Action: action, Value: value})
	return err
}
