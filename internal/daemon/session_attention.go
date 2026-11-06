package daemon

// rowAttention is why a displayed session row needs the person, nil when it
// does not; an open request outranks a failed message, which outranks the rest.
func rowAttention(row dashboardSession, outgoing int, incoming string) *SessionAttention {
	if row.Archived || row.Display == nil || row.Display.Kind == "unavailable" {
		return nil
	}
	if row.WaitingOn != nil {
		if row.ReplacedLead != nil {
			return &SessionAttention{Kind: AttentionReplacedRequest, Reason: row.Display.Detail}
		}
		return &SessionAttention{Kind: "request", Reason: row.Display.Detail}
	}
	if row.EndTurn.asksPerson() {
		return &SessionAttention{Kind: AttentionAsk, Reason: row.EndTurn.Headline}
	}
	if incoming != "" {
		return &SessionAttention{Kind: "incoming", Reason: incoming}
	}
	if outgoing > 0 {
		return &SessionAttention{Kind: "outgoing", Reason: CountText(outgoing, "message") + " not delivered"}
	}
	switch row.Display.Kind {
	case "limited":
		return &SessionAttention{Kind: "usageLimit", Reason: row.Display.Detail}
	case "stopped":
		return &SessionAttention{Kind: "stopped", Reason: row.Display.Detail}
	}
	if row.KeepAlive != nil {
		return &SessionAttention{Kind: AttentionKeepAliveFailed, Reason: keepAliveFailedText("This session")}
	}
	return nil
}
