package surface

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var claudeProjectMoveKeys = []string{
	"hasTrustDialogAccepted",
	"allowedTools",
	"mcpServers",
	"enabledMcpjsonServers",
	"disabledMcpjsonServers",
	"mcpContextUris",
	"hasClaudeMdExternalIncludesApproved",
	"hasClaudeMdExternalIncludesWarningShown",
	"ignorePatterns",
	"hasCompletedProjectOnboarding",
	"projectOnboardingSeenCount",
}

var claudeMCPServerCredentialKeys = []string{"env", "headers"}

func ClaudeProjectForMove(home, root, cwd string) (json.RawMessage, []string, error) {
	data, _, err := ReadClaudeSettingsFile(ClaudeGlobalConfigPath(home, root))
	if err != nil {
		return nil, nil, err
	}
	entry := gjson.GetBytes(data, claudeProjectPath(cwd))
	if !entry.IsObject() {
		return nil, nil, nil
	}
	carried := []byte("{}")
	for _, key := range claudeProjectMoveKeys {
		value := entry.Get(escapeSettingsPath(key))
		if !value.Exists() {
			continue
		}
		if carried, err = sjson.SetRawBytes(carried, escapeSettingsPath(key), []byte(value.Raw)); err != nil {
			return nil, nil, err
		}
	}
	var stripped []string
	gjson.GetBytes(carried, "mcpServers").ForEach(func(name, server gjson.Result) bool {
		if !server.IsObject() {
			return true
		}
		for _, key := range claudeMCPServerCredentialKeys {
			if !server.Get(key).Exists() {
				continue
			}
			path := "mcpServers." + escapeSettingsPath(name.String()) + "." + key
			if carried, err = sjson.DeleteBytes(carried, path); err != nil {
				return false
			}
			if !slices.Contains(stripped, name.String()) {
				stripped = append(stripped, name.String())
			}
		}
		return true
	})
	if err != nil {
		return nil, nil, err
	}
	if len(gjson.ParseBytes(carried).Map()) == 0 {
		return nil, nil, nil
	}
	slices.Sort(stripped)
	return carried, stripped, nil
}

func MergeMovedClaudeProject(home, root, cwd string, entry json.RawMessage) (func() error, error) {
	parsed := gjson.ParseBytes(entry)
	if !gjson.ValidBytes(entry) || !parsed.IsObject() {
		return nil, fmt.Errorf("the moved project's Claude settings are not a JSON object")
	}
	return mergeClaudeProjectInto(ClaudeGlobalConfigPath(home, root), cwd, parsed)
}

func CheckClaudeProjectTarget(home, root string) error {
	path := ClaudeGlobalConfigPath(home, root)
	data, _, err := ReadClaudeSettingsFile(path)
	if err != nil {
		return err
	}
	if projects := gjson.GetBytes(data, "projects"); projects.Exists() && !projects.IsObject() {
		return fmt.Errorf("%s has a projects value that is not an object; fix it first", path)
	}
	return nil
}
