package cli

import "github.com/zm2231/agenthail/internal/daemon"

func (a *App) daemonIsRunning() bool {
	if a.catalogDaemonRunning != nil {
		return a.catalogDaemonRunning()
	}
	_, running := daemon.IsRunning()
	return running
}
