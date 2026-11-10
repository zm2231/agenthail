package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

type principalKind string

const (
	principalPerson  principalKind = "person"
	principalDevice  principalKind = "device"
	principalHost    principalKind = "host"
	principalSession principalKind = "session"
)

func sessionPrincipal(sessionID string) principal {
	return principal{ID: "session:" + sessionID, Kind: principalSession, Claimed: sessionID}
}

const localDashboardPrincipal = "local-dashboard"

type principal struct {
	ID              string
	Kind            principalKind
	HostID          string
	HostName        string
	Claimed         string
	DashboardBearer bool
	// ForPerson is a session that answers for the person: their voice operator.
	ForPerson bool
}

func principalFromRequest(r *http.Request) principal {
	p := principal{ID: localDashboardPrincipal, Kind: principalPerson, Claimed: claimedActor(r.Header.Get(ActionActorHeader)), DashboardBearer: dashboardBearer(r.Context())}
	if id, ok := r.Context().Value(actionPrincipalContextKey{}).(string); ok && id != "" {
		p.ID = id
	}
	if host, ok := r.Context().Value(apiHostPrincipalContextKey{}).(apiHostPrincipal); ok {
		p.Kind, p.HostID, p.HostName = principalDevice, host.hostID, host.hostName
		if host.host {
			p.Kind = principalHost
		}
	}
	return p
}

// A claimed actor is trusted only from a paired host or the local dashboard token, which only the CLI holds.
func (p principal) actor() string {
	if p.Kind == principalSession {
		return "session " + p.Claimed
	}
	if p.Kind != principalPerson && strings.TrimSpace(p.HostName) != "" {
		if p.Kind == principalDevice {
			return fmt.Sprintf("device %s", p.HostName)
		}
		if p.Claimed != "" {
			return fmt.Sprintf("%s via host %s", p.Claimed, p.HostName)
		}
		return fmt.Sprintf("host %s", p.HostName)
	}
	if p.Claimed == CLIActor && p.DashboardBearer {
		return CLIActor
	}
	return "the dashboard"
}

func (p principal) context(ctx context.Context) context.Context {
	if p.ID != "" && p.ID != localDashboardPrincipal {
		ctx = context.WithValue(ctx, actionPrincipalContextKey{}, p.ID)
	}
	if p.Kind == principalHost || p.Kind == principalDevice {
		ctx = context.WithValue(ctx, apiHostPrincipalContextKey{}, apiHostPrincipal{host: p.Kind == principalHost, hostID: p.HostID, hostName: p.HostName})
	}
	if p.DashboardBearer {
		ctx = context.WithValue(ctx, apiDashboardBearerContextKey{}, true)
	}
	return ctx
}
