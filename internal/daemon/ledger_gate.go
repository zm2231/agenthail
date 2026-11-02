package daemon

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// ledgerManaged is whether G2 may queue into a session: one Agenthail started, never the person's own terminal or a delegated subagent.
func (d *Daemon) ledgerManaged(session surface.Session) bool {
	if session.Delegated() {
		return false
	}
	origin, found, err := d.Registry.SessionOrigin(session.ID)
	if err != nil || !found {
		return false
	}
	switch origin.Via {
	case registry.OriginAgent, registry.OriginCoordinator:
		return true
	}
	return session.RuntimeKind != surface.RuntimeClaudeCLI
}

func (d *Daemon) gateLedgerTurn(session surface.Session, turnID string) {
	if turnID == "" || !d.ledgerManaged(session) {
		return
	}
	store := d.ledgerStore()
	targets, _, err := d.ledgerTargets(store, session)
	if err != nil {
		d.log.Printf("ledger G2: projects of %s: %s", session.ID, err)
		return
	}
	for _, target := range targets {
		result, err := store.GateTurn(target.key, session.ID, turnID, time.Now())
		if err != nil {
			d.log.Printf("ledger G2: gate %s in %s: %s", session.ID, target.key, err)
			continue
		}
		if result.Result == "" {
			continue
		}
		d.publishEvent("ledger.gate", session.ID, result)
		if result.Result != ledger.GateBlocked {
			continue
		}
		key := fmt.Sprintf("ledger:g2:%s:%s:%s", session.ID, turnID, target.key)
		if _, err := d.Registry.QueueMessageWithKey(session.ID, gateNotice(result), key); err != nil {
			d.log.Printf("ledger G2: queue for %s: %s", session.ID, err)
		}
	}
}

func gateNotice(result ledger.GateResult) string {
	var lines []string
	texts := map[string]string{}
	for _, c := range result.Open {
		texts[c.ID] = c.Text
	}
	for _, item := range result.Blocked {
		if text, ok := texts[item]; ok {
			lines = append(lines, fmt.Sprintf("  %s: %s", item, text))
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s: the person's words, not yet attached", item))
	}
	return fmt.Sprintf(`[intent ledger] You ended your turn with open work in %s and no progress on it:
%s
Before you stop: report what changed (agenthail ledger progress <clause> --ref <commit|file|PR|evidence>), declare what you are waiting on (agenthail ledger wait <clause> --condition "..." --deadline 30m), or attach the person's words (agenthail ledger attach <utterance> --as ...).`, result.ProjectKey, strings.Join(lines, "\n"))
}

func (d *Daemon) apiLedgerClauseHandler(w http.ResponseWriter, r *http.Request) {
	var req ledger.ClauseRequest
	if !decodeLedgerWrite(w, r, &req) || !d.knownLedgerSession(w, req.SessionID) || !d.knownLedgerProject(w, req.ProjectKey) {
		return
	}
	if req.Owner != "" {
		if _, err := d.Registry.Session(req.Owner); err != nil {
			writeAPIError(w, http.StatusNotFound, "session_not_found", "No session "+req.Owner+" in this Agenthail.")
			return
		}
	}
	result, err := d.ledgerStore().ClauseAction(req, time.Now())
	if err != nil {
		writeLedgerError(w, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, result)
}
