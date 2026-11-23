package registry

import (
	"slices"
	"strings"
)

// AskOriginsOnRows are the ask origins a session's row shows, and so the Inbox built from rows.
var AskOriginsOnRows = []string{AskOriginAgent, AskOriginQuestion, AskOriginDelivery, AskOriginLedger, AskOriginApproval, AskOriginExtensionGrant}

var AskOriginsOffRows = map[string]string{
	AskOriginPermission:      "it mirrors the provider's own wait, which the row already shows as waitingOn",
	AskOriginExtensionChange: "nothing raises it any more; the daemon withdraws leftovers when it starts",
}

var rowOriginList = "'" + strings.Join(AskOriginsOnRows, "','") + "'"

var openPersonAskSessions = `SELECT session_id FROM asks WHERE state='open' AND origin IN (` + rowOriginList + `) AND (origin='` + AskOriginDelivery + `' OR routed_to='` + AskToPerson + `')`

func AskWaitsOnPerson(ask Ask) bool {
	return ask.State == AskOpen && slices.Contains(AskOriginsOnRows, ask.Origin) && (ask.Origin == AskOriginDelivery || ask.RoutedTo == AskToPerson)
}
