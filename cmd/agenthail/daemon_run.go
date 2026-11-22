package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/zm2231/agenthail/internal/browserhost"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/cli"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/nodehost"
	"github.com/zm2231/agenthail/internal/simhost"
	"github.com/zm2231/agenthail/internal/surface/claudesurface"
)

func runDaemon(run cli.DaemonRun) error {
	d := daemon.New(run.Registry, run.Surfaces)
	d.SetStartup(run.Began, time.Since(run.Began))
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	d.SetBuild(run.Build)
	d.SetClaudeHost(&claudehost.Manager{Home: home, Launch: func() (claudehost.Launch, error) {
		return claudesurface.ClaudeHostLaunch(home, run.HostVersion)
	}})
	d.SetBrowserHost(&browserhost.Manager{Home: home, Launch: func() (browserhost.Launch, error) {
		return browserhost.InstalledLaunch(run.HostVersion)
	}})
	d.SetSimulatorHost(simhost.New(simhost.Config{OutRoot: filepath.Join(home, ".agenthail", "simulator", "out")}))
	d.SetExtensionHost(home, func() (nodehost.Launch, error) {
		return nodehost.InstalledLaunch("extension host", "extension-host", "AGENTHAIL_EXTENSION_HOST", run.HostVersion)
	})
	return d.RunWithSignal()
}
