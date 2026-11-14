package cli

import (
	"context"
	"errors"
	"os"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/mcpstdio"
)

const mcpUsage = "usage: agenthail mcp stdio"

func (a *App) cmdMCP(args []string) error {
	if len(args) != 1 || args[0] != "stdio" {
		return errors.New(mcpUsage)
	}
	return mcpstdio.Run(context.Background(), daemon.MCPSocketPath(), a.Version, os.Stdin, os.Stdout)
}
