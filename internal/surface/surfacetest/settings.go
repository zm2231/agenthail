package surfacetest

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func SettingValue(t *testing.T, settings []surface.TurnSetting, key string) surface.TurnSetting {
	t.Helper()
	for _, setting := range settings {
		if setting.Key == key {
			return setting
		}
	}
	t.Fatalf("no %s setting in %+v", key, settings)
	return surface.TurnSetting{}
}

func CommandInserts(commands *surface.RuntimeCommands) []string {
	var out []string
	for _, command := range commands.Commands {
		out = append(out, command.Insert)
	}
	return out
}
