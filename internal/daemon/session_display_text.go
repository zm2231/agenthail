package daemon

func mirroredHostDown(row dashboardSession) bool {
	if row.Host == nil {
		return false
	}
	switch row.Host.State {
	case hostStateUnreachable, hostStateUnauthorized, hostStateIncompatible:
		return true
	}
	return false
}
