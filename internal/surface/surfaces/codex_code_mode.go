package surfaces

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/zm2231/agenthail/internal/surface/codemode"
)

const codexBackgroundStatus = "background"

var codexScriptHeader = regexp.MustCompile(`\A(Script completed|Script failed|Script running with cell ID \S+)\nWall time [^\n]*\nOutput:\n?`)

func codexCodeModeScript(payload map[string]any) ([]codemode.Call, bool) {
	if input, ok := payload["input"].(string); ok {
		return codemode.Read(input)
	}
	arguments, ok := payload["arguments"].(string)
	if !ok {
		return nil, false
	}
	var fields map[string]any
	if json.Unmarshal([]byte(arguments), &fields) != nil {
		return nil, false
	}
	for _, value := range fields {
		if script, ok := value.(string); ok && strings.Contains(script, "tools.") {
			if calls, ok := codemode.Read(script); ok {
				return calls, true
			}
		}
	}
	return nil, false
}

func codexScriptCommands(calls []codemode.Call) []string {
	var commands []string
	for _, call := range calls {
		if codemode.ToolName(call.Tool) == "exec_command" {
			if command := strings.TrimSpace(call.Fields["cmd"]); command != "" {
				commands = append(commands, command)
			}
		}
	}
	return commands
}

func codexScriptOutput(text string) (body, status string, ok bool) {
	header := codexScriptHeader.FindStringSubmatch(text)
	if header == nil {
		return text, "", false
	}
	body = strings.TrimLeft(text[len(header[0]):], "\n")
	switch {
	case header[1] == "Script failed":
		status = "failed"
	case strings.HasPrefix(header[1], "Script running"):
		status = codexBackgroundStatus
	}
	return body, status, true
}
