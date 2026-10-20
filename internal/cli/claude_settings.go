package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

const claudeSettingsUsage = "usage: agenthail claude settings compact [--root <path|name|email>] [--model <model>] [<tokens|auto|unset>] [--enabled on|off] [--idle on|off] [--json]"

func (a *App) cmdClaude(args []string) error {
	positional := stripFlags(args)
	if len(positional) < 2 || positional[0] != "settings" || positional[1] != "compact" || len(positional) > 3 {
		return fmt.Errorf("%s", claudeSettingsUsage)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	change := surface.ClaudeCompactChange{Model: flagVal(args, "--model")}
	if len(positional) == 3 {
		change.Window = positional[2]
	}
	for flag, target := range map[string]**bool{"--enabled": &change.Enabled, "--idle": &change.Idle} {
		if !hasFlag(args, flag) {
			continue
		}
		switch flagVal(args, flag) {
		case "on":
			*target = new(bool)
			**target = true
		case "off":
			*target = new(bool)
		default:
			return fmt.Errorf("%s takes on or off\n%s", flag, claudeSettingsUsage)
		}
	}
	changing := change.Window != "" || change.Model != "" || change.Enabled != nil || change.Idle != nil
	var roots []string
	if choice := flagVal(args, "--root"); choice != "" || changing {
		root, err := surface.ResolveClaudeAccountRoot(home, choice)
		if err != nil {
			return err
		}
		roots = []string{root}
	} else if roots, err = surface.ClaudeConfigRoots(home); err != nil {
		return err
	}
	var results []surface.ClaudeCompactSettings
	for _, root := range roots {
		var settings surface.ClaudeCompactSettings
		if changing {
			settings, err = surface.WriteClaudeCompactSettings(root, change)
		} else {
			settings, err = surface.ReadClaudeCompactSettings(root)
		}
		if err != nil {
			return err
		}
		results = append(results, settings)
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"roots": results})
	}
	for _, settings := range results {
		fmt.Println(describeClaudeCompactSettings(settings))
	}
	if changing {
		fmt.Println("Applies to Claude sessions started after the change.")
	}
	return nil
}

func describeClaudeCompactSettings(settings surface.ClaudeCompactSettings) string {
	lines := []string{settings.SettingsPath, "  autoCompactWindow: " + describeClaudeCompactWindow(settings.AutoCompactWindow)}
	models := make([]string, 0, len(settings.ModelWindows))
	for model := range settings.ModelWindows {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		window := settings.ModelWindows[model]
		lines = append(lines, fmt.Sprintf("  %s: %s", model, describeClaudeCompactWindow(&window)))
	}
	lines = append(lines, "  autoCompactEnabled: "+describeClaudeSettingBool(settings.AutoCompactEnabled), "  idleCompaction: "+describeClaudeSettingBool(settings.IdleCompaction))
	names := make([]string, 0, len(settings.Overrides))
	for name := range settings.Overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		lines = append(lines, fmt.Sprintf("  overridden: %s=%s is set in this environment and takes precedence", name, settings.Overrides[name]))
	}
	return strings.Join(lines, "\n")
}

func describeClaudeCompactWindow(window *surface.ClaudeCompactWindow) string {
	switch {
	case window == nil:
		return "unset"
	case window.Auto:
		return "auto"
	case window.Ignored != "":
		return window.Ignored + " (Claude ignores it)"
	}
	return fmt.Sprint(window.Tokens)
}

func describeClaudeSettingBool(value *bool) string {
	switch {
	case value == nil:
		return "unset"
	case *value:
		return "on"
	}
	return "off"
}
