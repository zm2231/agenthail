package surface

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const claudeRemoteControlAtStartup = "remoteControlAtStartup"

// ClaudeRemoteControl is whether a Claude config root starts Remote Control in
// each new session. AtStartup is nil when the root leaves it to Claude;
// Problem says why its settings file cannot be read or changed.
type ClaudeRemoteControl struct {
	Root         string `json:"root"`
	Name         string `json:"name"`
	SettingsPath string `json:"settingsPath"`
	AtStartup    *bool  `json:"atStartup,omitempty"`
	Problem      string `json:"problem,omitempty"`
}

func ReadClaudeRemoteControl(home string) ([]ClaudeRemoteControl, error) {
	roots, err := DescribeClaudeConfigRoots(home)
	if err != nil {
		return nil, err
	}
	settings := make([]ClaudeRemoteControl, 0, len(roots))
	for _, root := range roots {
		path := ClaudeSettingsPath(root.Root)
		setting := ClaudeRemoteControl{Root: root.Root, Name: root.Name, SettingsPath: path}
		data, _, err := ReadClaudeSettingsFile(path)
		if err != nil {
			setting.Problem = err.Error()
		} else if value := gjson.GetBytes(data, claudeRemoteControlAtStartup); value.IsBool() {
			on := value.Bool()
			setting.AtStartup = &on
		}
		settings = append(settings, setting)
	}
	return settings, nil
}

// WriteClaudeRemoteControl sets the root's remoteControlAtStartup and reports
// whether the file changed. Running sessions keep what they started with.
func WriteClaudeRemoteControl(root string, on bool) (bool, error) {
	path := ClaudeSettingsPath(root)
	data, mode, err := ReadClaudeSettingsFile(path)
	if err != nil {
		return false, err
	}
	if value := gjson.GetBytes(data, claudeRemoteControlAtStartup); value.IsBool() && value.Bool() == on {
		return false, nil
	}
	updated, err := sjson.SetBytes(data, claudeRemoteControlAtStartup, on)
	if err != nil {
		return false, err
	}
	return true, WriteClaudeSettingsFile(path, updated, mode)
}
