package codexsurface

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) desktopAppServer(ctx context.Context) (*surface.AppServer, error) {
	desktop, err := c.openDesktop(ctx)
	if err != nil {
		return nil, err
	}
	defer desktop.Close()
	host, reported := desktopLocalHost(ctx, desktop)
	switch {
	case reported && host.Fatal != "":
		return nil, fmt.Errorf("Codex Desktop's app-server stopped: %s", host.Fatal)
	case reported && host.State != "" && host.State != "connected":
		return nil, fmt.Errorf("Codex Desktop's app-server is %s", host.State)
	}
	if _, err := c.loadedThreadIDs(ctx, desktop, 1); err != nil {
		return nil, err
	}
	server := &surface.AppServer{Mode: c.desktopAppServerMode(ctx, desktop)}
	if reported {
		server.HostID, server.Version, server.Transport = host.HostID, host.AppServerVersion, host.Transport
	}
	if *server == (surface.AppServer{}) {
		return nil, nil
	}
	return server, nil
}

func codexServerProcess(ctx context.Context, client codexClient) (int64, error) {
	response, err := client.Request(ctx, "server/diagnostics", map[string]any{}, codexAppServerWait)
	if err != nil {
		return 0, err
	}
	result, _ := response["result"].(map[string]any)
	process, _ := result["process"].(map[string]any)
	id, _ := process["id"].(float64)
	if id <= 0 {
		return 0, fmt.Errorf("server/diagnostics named no app-server process")
	}
	return int64(id), nil
}

// One process answering both the Desktop bridge and the daemon socket is a shared server.
func (c *Codex) desktopAppServerMode(ctx context.Context, desktop codexClient) surface.AppServerMode {
	if !c.managed {
		return ""
	}
	desktopProcess, err := codexServerProcess(ctx, desktop)
	if err != nil {
		return ""
	}
	managed, err := c.dialManaged(ctx)
	if err != nil {
		return surface.AppServerPrivate
	}
	defer managed.Close()
	daemonProcess, err := codexServerProcess(ctx, managed)
	if err != nil {
		return ""
	}
	if daemonProcess == desktopProcess {
		return surface.AppServerShared
	}
	return surface.AppServerPrivate
}
