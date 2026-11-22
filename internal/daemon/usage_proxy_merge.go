package daemon

import (
	"os"

	"github.com/zm2231/agenthail/internal/daemon/usage"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) rootProxy(name string) *usage.Proxy {
	home, err := os.UserHomeDir()
	if err != nil {
		return &usage.Proxy{Name: name}
	}
	configs, _ := surface.ConfiguredProxies(home)
	for _, config := range configs {
		if config.Name == name {
			return d.proxyIdentity(config)
		}
	}
	return &usage.Proxy{Name: name}
}
