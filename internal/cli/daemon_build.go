package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/buildinfo"
)

// daemonBuild is what doctor reports about the build the running daemon came
// from, next to this command's own.
type daemonBuild struct {
	Daemon  *buildinfo.Identity `json:"daemon,omitempty"`
	Command buildinfo.Identity  `json:"command"`
	Detail  string              `json:"detail"`
	Error   string              `json:"error,omitempty"`
}

// checkDaemonBuild compares the running daemon's build with this command's. It
// returns a problem only when both builds name their commit and they differ.
func (a *App) checkDaemonBuild(ctx context.Context) (*daemonBuild, *serviceProblem) {
	if !a.daemonIsRunning() {
		return nil, nil
	}
	report := &daemonBuild{Command: a.build()}
	daemon, err := a.readDaemonBuild(ctx)
	if err != nil {
		report.Error = err.Error()
		report.Detail = fmt.Sprintf("could not read the running daemon's build: %s; this agenthail is %s", err, report.Command)
		return report, nil
	}
	report.Daemon = &daemon
	report.Detail = fmt.Sprintf("the running daemon is %s, this agenthail is %s", daemon, report.Command)
	same, known := buildinfo.SameBuild(daemon, report.Command)
	if !known || same {
		return report, nil
	}
	return report, &serviceProblem{Problem: "daemon_build_mismatch", Detail: report.Detail, Fix: "agenthail daemon restart"}
}

func (a *App) readDaemonBuild(ctx context.Context) (buildinfo.Identity, error) {
	client, err := a.daemonAPI()
	if err != nil {
		return buildinfo.Identity{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, _, err := client.call(callCtx, "GET", "/api/v1/version", nil, nil, nil)
	if err != nil {
		return buildinfo.Identity{}, err
	}
	var build buildinfo.Identity
	if err := json.Unmarshal(data, &build); err != nil {
		return buildinfo.Identity{}, err
	}
	if build.Version == "" {
		build.Version = "an older build"
	}
	if build.Revision == "" {
		build.Revision = "unknown"
	}
	return build, nil
}
