package surface

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/proxyacct"
)

func ConfiguredProxies(home string) ([]proxyacct.Config, error) {
	config, err := ReadAgenthailConfig(home)
	if err != nil {
		return nil, err
	}
	raw, ok := config["proxies"]
	if !ok {
		return nil, nil
	}
	proxies, err := proxyacct.ParseConfigs(raw)
	if err != nil {
		return nil, fmt.Errorf("Agenthail config %s: %w", AgenthailConfigPath(home), err)
	}
	return proxies, nil
}

// A root's requests go through a proxy when claudeConfigDirs names it, or
// when the base URL Claude Code uses from that root is a configured proxy's.
func claudeRootProxies(home string, roots []ClaudeConfigRoot) {
	proxies, err := ConfiguredProxies(home)
	if err != nil || len(proxies) == 0 {
		return
	}
	named := map[string]string{}
	if entries, err := configuredClaudeRootEntries(home); err == nil {
		for _, entry := range entries {
			if entry.Proxy != "" && filepath.IsAbs(entry.Path) {
				named[claudeRootIdentity(filepath.Clean(entry.Path))] = entry.Proxy
			}
		}
	}
	for i := range roots {
		if name := named[claudeRootIdentity(roots[i].Root)]; name != "" {
			for _, proxy := range proxies {
				if proxy.Name == name {
					roots[i].Proxy = name
				}
			}
			continue
		}
		if proxy, ok := proxyacct.Match(proxies, claudeRootBaseURL(roots[i].Root)); ok {
			roots[i].Proxy = proxy.Name
		}
	}
}

func claudeRootBaseURL(root string) string {
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if readClaudeJSON(filepath.Join(root, "settings.json"), &settings) {
		if url := strings.TrimSpace(settings.Env["ANTHROPIC_BASE_URL"]); url != "" {
			return url
		}
	}
	return strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
}
