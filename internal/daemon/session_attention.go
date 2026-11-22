package daemon

import "github.com/zm2231/agenthail/internal/daemonapi"

// rowAttention is why a displayed session row needs the person, nil when it
// does not; an open request outranks an ask, which outranks the rest.
func rowAttention(row dashboardSession) *daemonapi.SessionAttention {
	if row.Archived || row.Display == nil || row.Display.Kind == "unavailable" {
		return nil
	}
	if row.WaitingOn != nil {
		if row.ReplacedLead != nil {
			return &daemonapi.SessionAttention{Kind: daemonapi.AttentionReplacedRequest, Reason: row.Display.Detail}
		}
		return &daemonapi.SessionAttention{Kind: "request", Reason: row.Display.Detail}
	}
	if row.Ask.asksPerson() {
		return &daemonapi.SessionAttention{Kind: AttentionAsk, Reason: row.Ask.Title}
	}
	switch row.Display.Kind {
	case "limited":
		return &daemonapi.SessionAttention{Kind: "usageLimit", Reason: row.Display.Detail}
	case "stopped":
		return &daemonapi.SessionAttention{Kind: "stopped", Reason: row.Display.Detail}
	}
	return nil
}
