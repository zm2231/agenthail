package daemon

import (
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

func attachCodexConfigStore(reg *registry.Registry, adapters []surface.Surface) {
	for _, adapter := range adapters {
		if attachable, ok := adapter.(interface {
			SetCodexConfigStore(codexsurface.CodexConfigStore)
		}); ok {
			attachable.SetCodexConfigStore(reg)
		}
	}
}
