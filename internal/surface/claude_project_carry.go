package surface

import (
	"bytes"
	"fmt"
	"slices"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type claudeCarryAdd struct {
	path    string
	item    gjson.Result
	created bool
}

func CarryClaudeProject(home, from, to, cwd string) (func() error, error) {
	source, _, err := ReadClaudeSettingsFile(ClaudeGlobalConfigPath(home, from))
	if err != nil {
		return nil, err
	}
	entryPath := "projects." + escapeSettingsPath(cwd)
	entry := gjson.GetBytes(source, entryPath)
	if !entry.IsObject() {
		return func() error { return nil }, nil
	}
	path := ClaudeGlobalConfigPath(home, to)
	var added []claudeCarryAdd
	err = updateClaudeGlobalConfig(path, func(data []byte) ([]byte, error) {
		merged, adds, mergeErr := mergeClaudeProject(data, entryPath, entry)
		added = adds
		return merged, mergeErr
	})
	if err != nil || len(added) == 0 {
		return func() error { return nil }, err
	}
	return func() error {
		return updateClaudeGlobalConfig(path, func(data []byte) ([]byte, error) { return unmergeClaudeProject(data, added) })
	}, nil
}

func mergeClaudeProject(data []byte, entryPath string, entry gjson.Result) ([]byte, []claudeCarryAdd, error) {
	original := data
	var adds []claudeCarryAdd
	for _, container := range []string{"projects", entryPath} {
		have := gjson.GetBytes(data, container)
		if have.Exists() && !have.IsObject() {
			return original, nil, nil
		}
		if have.Exists() {
			continue
		}
		var err error
		if data, err = sjson.SetRawBytes(data, container, []byte("{}")); err != nil {
			return original, nil, err
		}
		adds = append(adds, claudeCarryAdd{path: container, created: true})
	}
	var err error
	entry.ForEach(func(key, value gjson.Result) bool {
		path := entryPath + "." + escapeSettingsPath(key.String())
		have := gjson.GetBytes(data, path)
		switch {
		case !have.Exists():
			data, err = sjson.SetRawBytes(data, path, []byte(value.Raw))
			adds = append(adds, claudeCarryAdd{path: path})
		case value.IsObject() && have.IsObject():
			value.ForEach(func(member, config gjson.Result) bool {
				memberPath := path + "." + escapeSettingsPath(member.String())
				if gjson.GetBytes(data, memberPath).Exists() {
					return true
				}
				data, err = sjson.SetRawBytes(data, memberPath, []byte(config.Raw))
				adds = append(adds, claudeCarryAdd{path: memberPath})
				return err == nil
			})
		case value.IsArray() && have.IsArray():
			for _, item := range value.Array() {
				if claudeArrayHas(gjson.GetBytes(data, path), item) {
					continue
				}
				if data, err = sjson.SetRawBytes(data, path+".-1", []byte(item.Raw)); err != nil {
					break
				}
				adds = append(adds, claudeCarryAdd{path: path, item: item})
			}
		}
		return err == nil
	})
	if err != nil {
		return original, nil, err
	}
	if !slices.ContainsFunc(adds, func(add claudeCarryAdd) bool { return !add.created }) {
		return original, nil, nil
	}
	return data, adds, nil
}

func unmergeClaudeProject(data []byte, added []claudeCarryAdd) ([]byte, error) {
	var err error
	for i := len(added) - 1; i >= 0 && err == nil; i-- {
		add := added[i]
		if add.created {
			if have := gjson.GetBytes(data, add.path); have.IsObject() && len(have.Map()) == 0 {
				data, err = sjson.DeleteBytes(data, add.path)
			}
			continue
		}
		if !add.item.Exists() {
			data, err = sjson.DeleteBytes(data, add.path)
			continue
		}
		items := gjson.GetBytes(data, add.path).Array()
		for index := len(items) - 1; index >= 0; index-- {
			if items[index].Raw == add.item.Raw {
				data, err = sjson.DeleteBytes(data, fmt.Sprintf("%s.%d", add.path, index))
				break
			}
		}
	}
	return data, err
}

func claudeArrayHas(array, item gjson.Result) bool {
	for _, have := range array.Array() {
		if have.Raw == item.Raw {
			return true
		}
	}
	return false
}

// Running Claude processes rewrite .claude.json too, so a change lands only on the bytes it read.
func updateClaudeGlobalConfig(path string, change func([]byte) ([]byte, error)) error {
	for attempt := 1; attempt <= 8; attempt++ {
		data, mode, err := ReadClaudeSettingsFile(path)
		if err != nil {
			return err
		}
		updated, err := change(bytes.Clone(data))
		if err != nil {
			return fmt.Errorf("update %s: %w", path, err)
		}
		if bytes.Equal(updated, data) {
			return nil
		}
		current, _, err := ReadClaudeSettingsFile(path)
		if err != nil {
			return err
		}
		if bytes.Equal(current, data) {
			return WriteClaudeSettingsFile(path, updated, mode)
		}
		time.Sleep(time.Duration(attempt) * 50 * time.Millisecond)
	}
	return fmt.Errorf("%s kept changing while Agenthail updated it; retry the move", path)
}
