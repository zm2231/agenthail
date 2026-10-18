package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"

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
	reg, err := registry.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open registry: %s\n", err)
		os.Exit(1)
	}
	defer reg.Close()

	helpers := sidecarAvailable(runtime.GOOS, os.Getenv("AGENTHAIL_SIDECAR"))
	claude := surfaces.NewClaude(envOr("AGENTHAIL_CHROME_PROFILE", "Default"), home)
	if !helpers {
		claude.DisableRemoteControl()
	}
	surfaces.SetChromeProfile(envOr("AGENTHAIL_CHROME_PROFILE", "Default"))
	codex := surfaces.NewCodex(codexconfig.RemoteDebuggingPort())
	entries := []cli.SurfaceEntry{
		{Name: "claude", Surface: claude},
		{Name: "codex", Surface: codex},
	}
	if helpers {
		entries = append(entries, cli.SurfaceEntry{Name: "notion", Surface: surfaces.NewNotion(
			envOr("AGENTHAIL_NOTION_SPACE", ""),
			envOr("AGENTHAIL_NOTION_USER", ""),
		)})
	}

	app := cli.App{
		Registry:       reg,
		Surfaces:       entries,
		DefaultTimeout: 2 * time.Minute,
		Version:        version,
		Revision:       revision,
		BuiltAt:        builtAt,
	}

	if err := app.Run(os.Args[1:]); err != nil {
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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
