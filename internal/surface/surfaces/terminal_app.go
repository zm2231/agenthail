package surfaces

import "bytes"

var terminalPrograms = map[string]string{
	"Apple_Terminal": "Terminal",
	"iTerm.app":      "iTerm2",
	"vscode":         "VS Code",
	"WezTerm":        "WezTerm",
	"ghostty":        "Ghostty",
	"tmux":           "tmux",
	"WarpTerminal":   "Warp",
	"zed":            "Zed",
}

// terminalApp names the terminal program process pid runs in, from the
// TERM_PROGRAM its terminal set, or "" when the platform does not tell.
func terminalApp(pid int) string {
	program := processEnv(pid, "TERM_PROGRAM")
	if name, ok := terminalPrograms[program]; ok {
		return name
	}
	return program
}

func envValue(entries [][]byte, name string) string {
	prefix := []byte(name + "=")
	for _, entry := range entries {
		if len(entry) == 0 {
			break
		}
		if value, ok := bytes.CutPrefix(entry, prefix); ok {
			return string(value)
		}
	}
	return ""
}
