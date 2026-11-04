package daemon

import "github.com/zm2231/agenthail/internal/deliverypolicy"

// The policy is read on every claim, so a problem is logged when it changes rather than each time.
func (d *Daemon) busyPolicy() deliverypolicy.Mode {
	mode, err := deliverypolicy.Load()
	problem := ""
	if err != nil {
		problem = err.Error()
	}
	if previous := d.busyPolicyProblem.Swap(&problem); (previous == nil && problem != "") || (previous != nil && *previous != problem) {
		if problem == "" {
			d.log.Printf("busy delivery setting readable again; using %s", mode)
		} else {
			d.log.Printf("busy delivery setting unreadable in %s; delivering as %s until it is fixed: %s", DashboardConfigPath(), mode, problem)
		}
	}
	return mode
}
