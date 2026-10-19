// Package surfacetest holds helpers for tests that fake surface adapters.
package surfacetest

import "github.com/zm2231/agenthail/internal/surface"

// RuntimeKinds are the runtimes the real adapter for kind serves, for fakes
// that stand in for it.
func RuntimeKinds(kind surface.SurfaceKind) []surface.RuntimeKind {
	switch kind {
	case surface.KindClaude:
		return []surface.RuntimeKind{surface.RuntimeClaudeCLI}
	case surface.KindCodex:
		return []surface.RuntimeKind{surface.RuntimeCodexDesktop, surface.RuntimeCodexAppServer}
	case surface.KindNotion:
		return []surface.RuntimeKind{surface.RuntimeNotionWeb}
	}
	return []surface.RuntimeKind{surface.RuntimeKind(kind)}
}
