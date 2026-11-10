package daemon

// rowAttention is why a displayed session row needs the person, nil when it
// does not; an open request outranks an ask, which outranks the rest.
func rowAttention(row dashboardSession) *SessionAttention {
	if row.Archived || row.Display == nil || row.Display.Kind == "unavailable" {
		return nil
	}
	if row.WaitingOn != nil {
		if row.ReplacedLead != nil {
			return &SessionAttention{Kind: AttentionReplacedRequest, Reason: row.Display.Detail}
		}
		return &SessionAttention{Kind: "request", Reason: row.Display.Detail}
	}
	if row.Ask.asksPerson() {
		return &SessionAttention{Kind: AttentionAsk, Reason: row.Ask.Title}
	}
	switch row.Display.Kind {
	case "limited":
		return &SessionAttention{Kind: "usageLimit", Reason: row.Display.Detail}
	case "stopped":
		return &SessionAttention{Kind: "stopped", Reason: row.Display.Detail}
	}
	return nil
}
