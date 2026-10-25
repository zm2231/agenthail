package daemon

import "fmt"

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

func CountText(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
