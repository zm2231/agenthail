package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/claudepeer"
	"github.com/zm2231/agenthail/internal/cli"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

var (
	version  = "dev"
	revision = "unknown"
	builtAt  = ""
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "claude-peer-worker" {
		var config claudepeer.Config
		if err := json.NewDecoder(os.Stdin).Decode(&config); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
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

	claude := surfaces.NewClaude(envOr("AGENTHAIL_CHROME_PROFILE", "Default"), home)
	surfaces.SetChromeProfile(envOr("AGENTHAIL_CHROME_PROFILE", "Default"))
	codex := surfaces.NewCodex(codexRemotePort())
	notion := surfaces.NewNotion(
		envOr("AGENTHAIL_NOTION_SPACE", ""),
		envOr("AGENTHAIL_NOTION_USER", ""),
	)

	app := cli.App{
		Registry: reg,
		Surfaces: []cli.SurfaceEntry{
			{Name: "claude", Surface: claude},
			{Name: "codex", Surface: codex},
			{Name: "notion", Surface: notion},
		},
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

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func codexRemotePort() string {
	return os.Getenv("AGENTHAIL_CODEX_INSPECT")
}
