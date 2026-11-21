package surfaces

import (
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

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

func effortLabel(level string) string {
	if level == "xhigh" {
		return "Extra high"
	}
	return strings.ToUpper(level[:1]) + level[1:]
}
