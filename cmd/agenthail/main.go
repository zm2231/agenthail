package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/zm2231/agenthail/internal/claudepeer"
	"github.com/zm2231/agenthail/internal/cli"
	"github.com/zm2231/agenthail/internal/codexconfig"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

var (
	version  = "dev"
	revision = "unknown"
	builtAt  = ""
)

func main() {
	if len(os.Args) == 3 && (os.Args[1] == "claude-peer-worker" || os.Args[1] == "claude-peer-relay") {
		var config claudepeer.Config
		if err := json.NewDecoder(os.Stdin).Decode(&config); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if config.ProcessToken == "" || os.Args[2] != config.ProcessToken {
			fmt.Fprintln(os.Stderr, "Claude peer worker launch token mismatch")
			os.Exit(1)
		}
		if os.Args[1] == "claude-peer-relay" {
			config.ReplyRelay = true
		}
		if err := claudepeer.RunWorker(context.Background(), config, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	home, _ := os.UserHomeDir()
	app := &cli.App{
		OpenRegistry:        func() (*registry.Registry, error) { return registry.OpenDefault(mayUpgradeRegistry(version)) },
		OpenCurrentRegistry: func() (*registry.Registry, error) { return registry.OpenCurrent("") },
		DefaultTimeout:      cli.DefaultCommandTimeout,
		Version:             version,
		Revision:            revision,
		BuiltAt:             builtAt,
	}

	helpers := sidecarAvailable(runtime.GOOS, os.Getenv("AGENTHAIL_SIDECAR"))
	claude := surfaces.NewClaude(app, home)
	if !helpers {
		claude.DisableRemoteControl()
	}
	codex := surfaces.NewCodex(codexconfig.RemoteDebuggingPort())
	app.Surfaces = []cli.SurfaceEntry{
		{Name: "claude", Surface: claude},
		{Name: "codex", Surface: codex},
	}
	if helpers {
		app.Surfaces = append(app.Surfaces, cli.SurfaceEntry{Name: "notion", Surface: surfaces.NewNotion(
			app,
			envOr("AGENTHAIL_NOTION_SPACE", ""),
			envOr("AGENTHAIL_NOTION_USER", ""),
		)})
	}

	err := app.Run(os.Args[1:])
	app.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

// sidecarAvailable reports whether this install has the sidecar that Notion
// and Claude Remote Control talk through. The Mac package ships it; the Linux
// archive does not, so off macOS it is present only when AGENTHAIL_SIDECAR
// names one.
func sidecarAvailable(goos, sidecar string) bool {
	return goos == "darwin" || sidecar != ""
}

// mayUpgradeRegistry is whether this build migrates the registry in the
// user's home: a release build does, a development build only when asked, so
// running one from a checkout cannot leave the installed agenthail a schema it
// refuses.
func mayUpgradeRegistry(version string) bool {
	return version != "dev" || os.Getenv("AGENTHAIL_ALLOW_DEV_MIGRATION") == "1"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
