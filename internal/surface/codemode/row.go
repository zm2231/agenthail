package codemode

import (
	"encoding/json"
	"regexp"
	"strings"
)

const ScriptTool = "Script"

type Row struct {
	Tool  string
	Input map[string]string
}

var namespacePattern = regexp.MustCompile(`^(functions\.|multi_agent_v[0-9]+__)`)

func ToolName(name string) string {
	return namespacePattern.ReplaceAllString(name, "")
}

// Present names the row for the script's first call; a script without one
// reads as a Script row listing the tools it calls.
func Present(calls []Call) Row {
	if len(calls) == 0 {
		return Row{Tool: ScriptTool, Input: map[string]string{"description": "Runs a script"}}
	}
	first := calls[0]
	if len(first.Fields) > 0 {
		return Row{Tool: ToolName(first.Tool), Input: first.Fields}
	}
	return Row{Tool: ToolName(first.Tool), Input: map[string]string{"description": "Calls " + toolList(calls)}}
}

func (r Row) Text() string {
	encoded, _ := json.Marshal(r.Input)
	return string(encoded)
}

func toolList(calls []Call) string {
	var names []string
	seen := map[string]bool{}
	for _, call := range calls {
		name := strings.ReplaceAll(ToolName(call.Tool), "__", " · ")
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}
