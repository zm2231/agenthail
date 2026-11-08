package surfaces

import (
	"maps"
	"sync"
)

type CodexConfigStore interface {
	CodexConfigOverrides(sessionID string) (map[string]any, error)
	SetCodexConfigOverride(sessionID, key string, value any) (map[string]any, error)
	RestoreCodexConfigOverrides(sessionID string, overrides map[string]any) error
}

type codexConfigAttachment struct {
	mu    sync.Mutex
	store CodexConfigStore
}

func (a *codexConfigAttachment) SetCodexConfigStore(store CodexConfigStore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.store = store
}

func (a *codexConfigAttachment) codexConfigStore() CodexConfigStore {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.store
}

func withConfig(params map[string]any, extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return params
	}
	merged := maps.Clone(params)
	config, _ := params["config"].(map[string]any)
	config = maps.Clone(config)
	if config == nil {
		config = map[string]any{}
	}
	maps.Copy(config, extra)
	merged["config"] = config
	return merged
}
