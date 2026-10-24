package cli

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/buildinfo"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (a *App) cmdVersion(args []string) error {
	if len(stripFlags(args)) != 0 {
		return fmt.Errorf("usage: agenthail version [--json]")
	}
	build := a.build()
	if hasFlag(args, "--json") {
		return emit(struct {
			buildinfo.Identity
			SchemaVersion int `json:"schemaVersion"`
		}{build, registry.SchemaVersion()})
	}
	fmt.Printf("agenthail %s (%s", build.Version, build.Revision)
	if build.Modified {
		fmt.Print(", modified")
	}
	fmt.Println(")")
	return nil
}

func (a *App) build() buildinfo.Identity {
	return buildinfo.Resolve(a.Version, a.Revision, a.BuiltAt)
}

func (a *App) allSurfaces() []surface.Surface {
	out := make([]surface.Surface, len(a.Surfaces))
	for i, e := range a.Surfaces {
		out[i] = e.Surface
	}
	return out
}

func (a *App) adapters() surface.Adapters { return surface.Adapters(a.allSurfaces()) }
