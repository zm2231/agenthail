package surface

import (
	"strings"
)

func NewModelSetting(model, source string, models []ModelOption) *ModelSetting {
	if model == "" {
		return nil
	}
	setting := &ModelSetting{Value: model, Source: source}
	if models != nil {
		listed := false
		for _, option := range models {
			listed = listed || option.ID == model
		}
		setting.Listed = &listed
	}
	return setting
}

func EffortLabel(level string) string {
	if level == "xhigh" {
		return "Extra high"
	}
	return strings.ToUpper(level[:1]) + level[1:]
}
