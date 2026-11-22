package cli

import (
	"github.com/zm2231/agenthail/internal/daemonctl"
)

func (a *App) daemonIsRunning() bool {
	if a.catalogDaemonRunning != nil {
		return a.catalogDaemonRunning()
	}
	_, running := daemonctl.IsRunning()
	return running
}
