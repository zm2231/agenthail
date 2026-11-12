package cli

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/zm2231/agenthail/internal/simhost"
)

var doctorSimulators = func(ctx context.Context) *simhost.Status {
	if runtime.GOOS != "darwin" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	status := simhost.New(simhost.Config{}).Status(ctx)
	return &status
}

func simulatorsLine(status *simhost.Status) string {
	switch {
	case status == nil:
		return ""
	case status.Ready && status.Simslim:
		return fmt.Sprintf("simulators: ready (AXe %s, %s, slimmed with simslim)", status.AXe, status.Runtime)
	case status.Ready:
		return fmt.Sprintf("simulators: ready (AXe %s, %s; brew install mobai-app/tap/simslim makes new devices lighter)", status.AXe, status.Runtime)
	case status.Fix != "":
		return fmt.Sprintf("simulators: unavailable to agents: %s; fix: %s", status.Problem, status.Fix)
	default:
		return "simulators: unavailable to agents: " + status.Problem
	}
}
