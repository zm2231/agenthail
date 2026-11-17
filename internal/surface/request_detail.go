package surface

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const detailChangeLines = 12

// Detail is what a request asks to do, written for someone deciding whether
// to allow it: the command for a shell, the path and a summary of the change
// for a file edit, and the tool input otherwise. Questions and input forms
// carry their own text and have none.
func (r Request) Detail() string {
	if r.Kind == WaitingQuestion || r.Kind == WaitingAsk || r.Kind == WaitingInput || len(r.Input) == 0 {
		return ""
	}
	var input map[string]any
	if json.Unmarshal(r.Input, &input) != nil || len(input) == 0 {
		return compactJSON(r.Input)
	}
	text := func(key string) string {
		value, _ := input[key].(string)
		return value
	}
	switch r.Tool {
	case "Bash":
		if command := text("command"); command != "" {
			if cwd := text("cwd"); cwd != "" {
				return command + "\n(in " + cwd + ")"
			}
			return command
		}
	case "Edit":
		if path := text("file_path"); path != "" {
			return changeDetail(path, []change{{removed: text("old_string"), added: text("new_string")}})
		}
	case "MultiEdit":
		if path := text("file_path"); path != "" {
			var changes []change
			edits, _ := input["edits"].([]any)
			for _, raw := range edits {
				edit, _ := raw.(map[string]any)
				removed, _ := edit["old_string"].(string)
				added, _ := edit["new_string"].(string)
				changes = append(changes, change{removed: removed, added: added})
			}
			return changeDetail(path, changes)
		}
	case "Write":
		if path := text("file_path"); path != "" {
			return changeDetail(path, []change{{added: text("content")}})
		}
	case "NotebookEdit":
		if path := text("notebook_path"); path != "" {
			return changeDetail(path, []change{{added: text("new_source")}})
		}
	}
	return compactJSON(r.Input)
}

type change struct{ removed, added string }

func changeDetail(path string, changes []change) string {
	removed, added := 0, 0
	var shown []string
	for _, c := range changes {
		before, after := detailLines(c.removed), detailLines(c.added)
		removed, added = removed+len(before), added+len(after)
		for _, line := range before {
			shown = append(shown, "- "+line)
		}
		for _, line := range after {
			shown = append(shown, "+ "+line)
		}
	}
	summary := fmt.Sprintf("%s: +%d -%d lines", path, added, removed)
	if len(changes) > 1 {
		summary += fmt.Sprintf(" in %d edits", len(changes))
	}
	if len(shown) > detailChangeLines {
		shown = append(slices.Clip(shown[:detailChangeLines]), fmt.Sprintf("… %d more lines", len(shown)-detailChangeLines))
	}
	return strings.Join(append([]string{summary}, shown...), "\n")
}

func detailLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func compactJSON(raw json.RawMessage) string {
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return out.String()
}
