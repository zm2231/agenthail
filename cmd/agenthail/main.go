package main

import (
	"fmt"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/cli"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func main() {
	home, _ := os.UserHomeDir()
	reg, err := registry.Open("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open registry: %s\n", err)
		os.Exit(1)
	}
	defer reg.Close()

	claude := surfaces.NewClaude(envOr("AGENTHAIL_CHROME_PROFILE", "Default"), home)
	surfaces.SetChromeProfile(envOr("AGENTHAIL_CHROME_PROFILE", "Default"))
	codex := surfaces.NewCodex("")

	app := cli.App{
		Registry: reg,
		Surfaces: []cli.SurfaceEntry{
			{Name: "claude", Surface: claude},
			{Name: "codex", Surface: codex},
		},
		DefaultTimeout: 30 * time.Second,
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
