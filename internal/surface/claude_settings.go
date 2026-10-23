package surface

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/zm2231/agenthail/internal/atomicfile"
)

func ClaudeSettingsPath(root string) string {
	return filepath.Join(root, "settings.json")
}

func ReadClaudeSettingsFile(path string) ([]byte, os.FileMode, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []byte("{}\n"), 0600, nil
	}
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	if strings.TrimSpace(string(data)) == "" {
		data = []byte("{}\n")
	}
	if !gjson.ValidBytes(data) || !gjson.ParseBytes(data).IsObject() {
		return nil, 0, fmt.Errorf("%s is not a JSON object; fix it first", path)
	}
	return data, info.Mode().Perm(), nil
}

// A symlinked settings file is written at its target so the link survives.
func WriteClaudeSettingsFile(path string, data []byte, mode os.FileMode) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return atomicfile.Write(path, data, mode)
}

// Claude accepts an autoCompactWindow of this many tokens, or "auto"; it
// ignores any other value.
const (
	ClaudeAutoCompactMinTokens = 100_000
	ClaudeAutoCompactMaxTokens = 1_000_000
)

// Ignored keeps, as written, a value Claude skips as if it were absent.
type ClaudeCompactWindow struct {
	Tokens  int64  `json:"tokens,omitempty"`
	Auto    bool   `json:"auto,omitempty"`
	Ignored string `json:"ignored,omitempty"`
}

// Overrides holds the variables in the daemon's environment that take
// precedence over the file for a Claude launched from it.
type ClaudeCompactSettings struct {
	Root               string                         `json:"root"`
	SettingsPath       string                         `json:"settingsPath"`
	AutoCompactWindow  *ClaudeCompactWindow           `json:"autoCompactWindow,omitempty"`
	ModelWindows       map[string]ClaudeCompactWindow `json:"modelWindows,omitempty"`
	AutoCompactEnabled *bool                          `json:"autoCompactEnabled,omitempty"`
	IdleCompaction     *bool                          `json:"idleCompaction,omitempty"`
	Overrides          map[string]string              `json:"overrides,omitempty"`
}

var claudeCompactOverrides = []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "DISABLE_AUTO_COMPACT", "DISABLE_COMPACT"}

func ReadClaudeCompactSettings(root string) (ClaudeCompactSettings, error) {
	path := ClaudeSettingsPath(root)
	data, _, err := ReadClaudeSettingsFile(path)
	if err != nil {
		return ClaudeCompactSettings{}, err
	}
	settings := ClaudeCompactSettings{Root: root, SettingsPath: path}
	if value := gjson.GetBytes(data, "autoCompactWindow"); value.Exists() {
		window := claudeCompactWindow(value)
		settings.AutoCompactWindow = &window
	}
	gjson.GetBytes(data, "modelSettings").ForEach(func(model, entry gjson.Result) bool {
		if value := entry.Get("autoCompactWindow"); model.Type == gjson.String && value.Exists() {
			if settings.ModelWindows == nil {
				settings.ModelWindows = map[string]ClaudeCompactWindow{}
			}
			settings.ModelWindows[model.String()] = claudeCompactWindow(value)
		}
		return true
	})
	settings.AutoCompactEnabled = claudeSettingsBool(data, "autoCompactEnabled")
	settings.IdleCompaction = claudeSettingsBool(data, "idleCompaction")
	for _, name := range claudeCompactOverrides {
		if value, ok := os.LookupEnv(name); ok {
			if settings.Overrides == nil {
				settings.Overrides = map[string]string{}
			}
			settings.Overrides[name] = value
		}
	}
	return settings, nil
}

func claudeCompactWindow(value gjson.Result) ClaudeCompactWindow {
	switch {
	case value.Type == gjson.String && value.String() == "auto":
		return ClaudeCompactWindow{Auto: true}
	case value.Type == gjson.Number && value.Num == float64(value.Int()) && value.Int() >= ClaudeAutoCompactMinTokens && value.Int() <= ClaudeAutoCompactMaxTokens:
		return ClaudeCompactWindow{Tokens: value.Int()}
	}
	return ClaudeCompactWindow{Ignored: value.Raw}
}

func claudeSettingsBool(data []byte, key string) *bool {
	value := gjson.GetBytes(data, key)
	if value.Type != gjson.True && value.Type != gjson.False {
		return nil
	}
	set := value.Bool()
	return &set
}

// Window is a token count, "auto", or "unset" to remove the key; Model
// scopes it to that model's modelSettings entry.
type ClaudeCompactChange struct {
	Model   string
	Window  string
	Enabled *bool
	Idle    *bool
}

// A running Claude does not reread the file; the change applies to sessions
// started after it.
func WriteClaudeCompactSettings(root string, change ClaudeCompactChange) (ClaudeCompactSettings, error) {
	path := ClaudeSettingsPath(root)
	data, mode, err := ReadClaudeSettingsFile(path)
	if err != nil {
		return ClaudeCompactSettings{}, err
	}
	updated := data
	if change.Window != "" {
		key := "autoCompactWindow"
		if model := strings.TrimSpace(change.Model); model != "" {
			key = "modelSettings." + escapeSettingsPath(model) + ".autoCompactWindow"
		}
		switch window := strings.TrimSpace(change.Window); window {
		case "unset":
			updated, err = sjson.DeleteBytes(updated, key)
		case "auto":
			updated, err = sjson.SetBytes(updated, key, "auto")
		default:
			tokens, parseErr := strconv.ParseInt(window, 10, 64)
			if parseErr != nil || tokens < ClaudeAutoCompactMinTokens || tokens > ClaudeAutoCompactMaxTokens {
				return ClaudeCompactSettings{}, fmt.Errorf("autoCompactWindow must be a token count from %d to %d, auto, or unset; got %q", ClaudeAutoCompactMinTokens, ClaudeAutoCompactMaxTokens, window)
			}
			updated, err = sjson.SetBytes(updated, key, tokens)
		}
		if err != nil {
			return ClaudeCompactSettings{}, err
		}
	} else if strings.TrimSpace(change.Model) != "" {
		return ClaudeCompactSettings{}, errors.New("a model applies only to autoCompactWindow; give a window")
	}
	for key, value := range map[string]*bool{"autoCompactEnabled": change.Enabled, "idleCompaction": change.Idle} {
		if value == nil {
			continue
		}
		if updated, err = sjson.SetBytes(updated, key, *value); err != nil {
			return ClaudeCompactSettings{}, err
		}
	}
	if string(updated) != string(data) {
		if err := WriteClaudeSettingsFile(path, updated, mode); err != nil {
			return ClaudeCompactSettings{}, err
		}
	}
	return ReadClaudeCompactSettings(root)
}

func escapeSettingsPath(key string) string {
	var escaped strings.Builder
	if strings.Trim(key, "0123456789") == "" {
		escaped.WriteByte(':')
	}
	for _, r := range key {
		if strings.ContainsRune(`\.*?|#@!=<>%:`, r) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}
