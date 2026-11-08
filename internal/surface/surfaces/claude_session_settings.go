package surfaces

import (
	"context"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) SessionSettings(ctx context.Context, sess *surface.Session) (surface.RuntimeSettings, error) {
	var settings surface.RuntimeSettings
	models, err := c.Models(ctx)
	if err != nil {
		models = nil
	}
	if claudeHosted(sess) {
		if listed, ok := c.host.session(ctx, sess.ID, sess.PID); ok {
			settings.Model = modelSetting(listed.Model, surface.SettingFromRuntime, models)
			if listed.Effort != "" {
				settings.Effort = &surface.SettingValue{Value: listed.Effort, Source: surface.SettingFromRuntime}
			} else {
				settings.Missing("effort", "the Claude host reports no effort for this session")
			}
			if listed.PermissionMode != "" {
				settings.Permission = &surface.PermissionSetting{Mode: listed.PermissionMode, Source: surface.SettingFromRuntime}
			}
		}
	}
	if settings.Model == nil {
		read := *sess
		if c.reachableAsIs(ctx, &read) && read.Bridge != "" {
			if model, err := c.remoteState(ctx, &read); err == nil && model != "" {
				settings.Model = modelSetting(model, surface.SettingFromRuntime, models)
			}
		}
	}
	path := sess.Transcript
	if path == "" {
		path = c.transcriptPath(sess)
	}
	if settings.Model == nil {
		if model, err := readClaudeLatestModel(ctx, path); err == nil && model != "" {
			settings.Model = modelSetting(model, surface.SettingFromTranscript, models)
		} else {
			settings.Missing("model", claudeTranscriptMissing(path, "no reply names a model yet"))
		}
	}
	if settings.Permission == nil {
		if mode := readClaudeLatestPermissionMode(ctx, path); mode != "" {
			settings.Permission = &surface.PermissionSetting{Mode: mode, Source: surface.SettingFromTranscript}
		} else {
			settings.Missing("permission", claudeTranscriptMissing(path, "no prompt records a permission mode yet"))
		}
	}
	if settings.Effort == nil && settings.Unavailable["effort"] == "" {
		settings.Missing("effort", "only a session the Claude host runs reports its effort")
	}
	return settings, nil
}

func claudeTranscriptMissing(path, otherwise string) string {
	if path == "" {
		return "the conversation has no transcript on this machine"
	}
	return "the transcript has " + otherwise
}

func modelSetting(model, source string, models []surface.ModelOption) *surface.ModelSetting {
	if model == "" {
		return nil
	}
	setting := &surface.ModelSetting{Value: model, Source: source}
	if models != nil {
		listed := false
		for _, option := range models {
			listed = listed || option.ID == model
		}
		setting.Listed = &listed
	}
	return setting
}

func readClaudeLatestPermissionMode(ctx context.Context, path string) string {
	if path == "" {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	match := func(record map[string]any) bool { return str(record, "type") == "user" && str(record, "permissionMode") != "" }
	start, found, err := transcriptRecordBefore(ctx, file, info.Size(), lineContains([]byte(`"permissionMode"`)), match)
	if err != nil || !found {
		return ""
	}
	mode := ""
	_ = scanTranscriptRecords(ctx, file, start, info.Size(), true, func(record map[string]any, _ int64, _ []byte) {
		if mode == "" && match(record) {
			mode = str(record, "permissionMode")
		}
	})
	return mode
}
