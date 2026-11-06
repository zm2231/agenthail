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

// agenthailStarted is a session Agenthail started, never the person's own terminal or a delegated subagent.
func (d *Daemon) agenthailStarted(session surface.Session) bool {
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
	case registry.OriginDaemon:
		return false
	}
	return session.RuntimeKind != surface.RuntimeClaudeCLI
}

func (d *Daemon) gateLedgerTurn(session surface.Session, turnID string) {
	if turnID == "" || !d.agenthailStarted(session) {
		return
	}
	store := d.ledgerStore()
	targets, access, err := d.ledgerTargets(store, session)
	if err != nil {
		d.log.Printf("ledger G2: projects of %s: %s", session.ID, err)
		return
	}
	var blocked []ledger.GateResult
	for _, target := range targets {
		scope, err := d.ledgerSince(store, target, session.ID, access)
		if err != nil {
			d.log.Printf("ledger G2: scope of %s in %s: %s", session.ID, target.key, err)
			continue
		}
		if !scope.hold {
			continue
		}
		result, err := store.GateTurn(target.key, session.ID, turnID, time.Now())
		if err != nil {
			d.log.Printf("ledger G2: gate %s in %s: %s", session.ID, target.key, err)
			continue
		}
		if result.Repeat {
			continue
		}
		d.countG2Check(target.key, result.Result != "")
		if result.Result == "" {
			continue
		}
		d.publishEvent("ledger.gate", session.ID, result)
		if result.Result == ledger.GateBlocked {
			blocked = append(blocked, result)
		}
	}
	if len(blocked) == 0 {
		return
	}
	key := fmt.Sprintf("ledger:g2:%s:%s", session.ID, turnID)
	if _, err := d.Registry.QueueMessageWithKey(session.ID, gateNotice(blocked), key); err != nil {
		d.log.Printf("ledger G2: queue for %s: %s", session.ID, err)
	}
}

func gateNotice(results []ledger.GateResult) string {
	var lines []string
	for _, result := range results {
		texts := map[string]string{}
		for _, c := range result.Open {
			texts[c.ID] = c.Text
		}
		lines = append(lines, "  in "+result.ProjectKey+":")
		for _, item := range result.Blocked {
			if text, ok := texts[item]; ok {
				lines = append(lines, fmt.Sprintf("    %s: %s", item, text))
				continue
			}
			lines = append(lines, fmt.Sprintf("    %s: the person's words, not yet attached", item))
		}
	}
	return fmt.Sprintf(`[intent ledger] You ended your turn with open work and no progress on it:
%s
Before you stop: report what changed (agenthail ledger progress <clause> --ref <commit|file|PR|evidence>), declare what you are waiting on (agenthail ledger wait <clause> --condition "..." --deadline 30m), or attach the person's words (agenthail ledger attach <utterance> --as ...).`, strings.Join(lines, "\n"))
}

func (d *Daemon) apiLedgerClauseHandler(w http.ResponseWriter, r *http.Request) {
	var req ledger.ClauseRequest
	if !decodeLedgerWrite(w, r, &req) || !d.knownLedgerSession(w, r, req.SessionID) || !d.knownLedgerProject(w, r, req.ProjectKey, req.SessionID) {
		return
	}
	if req.Owner != "" && ledgerProxiedBy(r) == "" {
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
