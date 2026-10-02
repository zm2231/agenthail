package surfaces

import "github.com/zm2231/agenthail/internal/claudeconfig"

func NewClaudeWithConfigDirs(profile, home string, dirs []string) (*Claude, error) {
	roots, err := claudeconfig.Normalize(dirs)
	if err != nil {
		return nil, err
	}
	adapter := NewClaude(profile, home)
	if len(roots) != 0 {
		adapter.configDirs = roots
	}
	return adapter, nil
}
