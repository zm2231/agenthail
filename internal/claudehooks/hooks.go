package claudehooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/zm2231/agenthail/internal/surface"
)

const execPrefix = "exec "

type Hook struct {
	Event   string
	Args    string
	Matcher string
	Timeout int64
}

// The prompt hook's timeout is how long, in seconds, Claude lets it hold a
// prompt. The hook ends on its own once the prompt is settled anywhere, so
// the limit only matters while the person is away.
var (
	Prompt       = Hook{Event: "PermissionRequest", Args: "hook permission-request", Matcher: "*", Timeout: 86400}
	Start        = Hook{Event: "SessionStart", Args: "hook session-context", Timeout: 10}
	PromptSubmit = Hook{Event: "UserPromptSubmit", Args: "hook session-context", Timeout: 10}
	All          = []Hook{Prompt, Start, PromptSubmit}
)

// The states of Agenthail's hooks in a settings file. An outdated hook is
// one install wrote before it ran hooks with exec, with another timeout, or
// under a matcher that narrows it; a file with some of the hooks is outdated
// too. Install brings it up to date.
const (
	Missing   = "missing"
	Outdated  = "outdated"
	Installed = "installed"
)

// Command is the hook command that runs executable. Claude runs hook commands
// with /bin/sh -c, and the prompt hook finds its Claude process as its parent;
// exec keeps a shell such as dash, which does not replace itself with a lone
// command, from becoming that parent.
func (h Hook) Command(executable string) string {
	return execPrefix + "'" + strings.ReplaceAll(executable, "'", `'\''`) + "' " + h.Args
}

// Owns reports whether a hook command is this hook as install writes it:
// this executable, or another agenthail executable, quoted as install quotes
// it and followed by exactly the hook's arguments.
func (h Hook) Owns(command string) bool {
	command = strings.TrimPrefix(command, execPrefix)
	if executable, err := os.Executable(); err == nil && command == strings.TrimPrefix(h.Command(executable), execPrefix) {
		return true
	}
	executable, found := strings.CutSuffix(command, " "+h.Args)
	if !found || len(executable) < 2 || executable[0] != '\'' || executable[len(executable)-1] != '\'' {
		return false
	}
	path := strings.ReplaceAll(executable[1:len(executable)-1], `'\''`, "'")
	return filepath.IsAbs(path) && filepath.Base(path) == "agenthail"
}

func InstallAt(path, executable string) (bool, error) {
	data, mode, err := surface.ReadClaudeSettingsFile(path)
	if err != nil {
		return false, err
	}
	updated := data
	for _, h := range All {
		if updated, err = h.install(updated, h.Command(executable)); err != nil {
			return false, err
		}
	}
	if string(updated) == string(data) {
		return false, nil
	}
	return true, surface.WriteClaudeSettingsFile(path, updated, mode)
}

// install moves the hook out of an entry whose matcher narrows it, since
// that matcher may be another hook's, and Agenthail's hooks run for all.
func (h Hook) install(data []byte, command string) ([]byte, error) {
	data, err := h.remove(data, func(entry gjson.Result) bool { return !matchesAll(entry) })
	if err != nil {
		return nil, err
	}
	found := false
	for i, entry := range gjson.GetBytes(data, "hooks."+h.Event).Array() {
		for j, hook := range entry.Get("hooks").Array() {
			if !h.Owns(hook.Get("command").String()) {
				continue
			}
			found = true
			prefix := fmt.Sprintf("hooks.%s.%d.hooks.%d.", h.Event, i, j)
			if hook.Get("command").String() != command {
				if data, err = sjson.SetBytes(data, prefix+"command", command); err != nil {
					return nil, err
				}
			}
			if hook.Get("timeout").Int() != h.Timeout {
				if data, err = sjson.SetBytes(data, prefix+"timeout", h.Timeout); err != nil {
					return nil, err
				}
			}
		}
	}
	if found {
		return data, nil
	}
	entry := map[string]any{"hooks": []map[string]any{{"type": "command", "command": command, "timeout": h.Timeout}}}
	if h.Matcher != "" {
		entry["matcher"] = h.Matcher
	}
	return sjson.SetBytes(data, "hooks."+h.Event+".-1", entry)
}

func UninstallAt(path string) (bool, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	}
	data, mode, err := surface.ReadClaudeSettingsFile(path)
	if err != nil {
		return false, err
	}
	updated := data
	for _, h := range All {
		if updated, err = h.remove(updated, func(gjson.Result) bool { return true }); err != nil {
			return false, err
		}
	}
	if string(updated) == string(data) {
		return false, nil
	}
	return true, surface.WriteClaudeSettingsFile(path, updated, mode)
}

func matchesAll(entry gjson.Result) bool {
	matcher := entry.Get("matcher").String()
	return matcher == "" || matcher == "*"
}

func (h Hook) remove(data []byte, from func(entry gjson.Result) bool) ([]byte, error) {
	var err error
	entries := gjson.GetBytes(data, "hooks."+h.Event).Array()
	for i := len(entries) - 1; i >= 0; i-- {
		if !from(entries[i]) {
			continue
		}
		hooks := entries[i].Get("hooks").Array()
		kept := 0
		for j := len(hooks) - 1; j >= 0; j-- {
			if !h.Owns(hooks[j].Get("command").String()) {
				kept++
				continue
			}
			if data, err = sjson.DeleteBytes(data, fmt.Sprintf("hooks.%s.%d.hooks.%d", h.Event, i, j)); err != nil {
				return nil, err
			}
		}
		if kept == 0 && len(hooks) > 0 {
			if data, err = sjson.DeleteBytes(data, fmt.Sprintf("hooks.%s.%d", h.Event, i)); err != nil {
				return nil, err
			}
		}
	}
	return data, nil
}

func AllStateAt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Missing, nil
	}
	if err != nil {
		return "", err
	}
	states := map[string]int{}
	for _, h := range All {
		states[h.State(data)]++
	}
	switch len(All) {
	case states[Installed]:
		return Installed, nil
	case states[Missing]:
		return Missing, nil
	}
	return Outdated, nil
}

func StateAt(path string, h Hook) (string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Missing, nil
	}
	if err != nil {
		return "", err
	}
	return h.State(data), nil
}

func (h Hook) State(data []byte) string {
	state := Missing
	for _, entry := range gjson.GetBytes(data, "hooks."+h.Event).Array() {
		for _, hook := range entry.Get("hooks").Array() {
			command := hook.Get("command").String()
			if !h.Owns(command) {
				continue
			}
			if matchesAll(entry) && strings.HasPrefix(command, execPrefix) && hook.Get("timeout").Int() == h.Timeout {
				return Installed
			}
			state = Outdated
		}
	}
	return state
}
