package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/ledger"
)

const takeoverJudgeTimeout = 10 * time.Second

// stampTakeover writes this host's judgment of a takeover's holder into the
// request before it is recorded here or sent to the ledger's owner, which
// trusts a paired host's stamp and replays it without judging again. Whatever
// the caller put there is replaced.
func (d *Daemon) stampTakeover(ctx context.Context, path string, body []byte) ([]byte, error) {
	if path != "/api/v1/ledger/work" {
		return body, nil
	}
	var req ledger.WorkRequest
	if json.Unmarshal(body, &req) != nil || req.Action != ledger.WorkTakeover || req.Takeover == nil {
		return body, nil
	}
	holder := ""
	if board, err := d.ledgerStore().Board(req.ProjectKey); err == nil {
		if item, found := board.WorkItem(req.ItemID); found {
			holder = item.Assignee
		}
	}
	judged := ledger.Takeover{Then: req.Takeover.Then, Force: req.Takeover.Force}
	if holder != "" {
		judged.Holder, judged.Verdict = holder, d.holderVerdict(ctx, holder)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(judged)
	if err != nil {
		return nil, err
	}
	fields["takeover"] = encoded
	return json.Marshal(fields)
}

// holderVerdict asks the host a holder runs on whether it is archived, still
// there, or gone. A session row mirrored from a paired host only says which
// host to ask; a holder no host claims is gone only when every paired host
// answered.
func (d *Daemon) holderVerdict(ctx context.Context, holder string) string {
	ctx, cancel := context.WithTimeout(ctx, takeoverJudgeTimeout)
	defer cancel()
	candidates := d.pairedHosts()
	if hostID, id, remote := hosts.Split(holder); remote && hostID != d.localHost().ID {
		host, found := d.pairedHost(hostID)
		if !found {
			return ledger.HolderUnknown
		}
		return remoteHolderVerdict(ctx, []*pairedHost{host}, id)
	} else if remote {
		holder = id
	}
	owner, err := d.Registry.SessionHost(holder)
	switch {
	case err == nil && owner != "" && owner != d.localHost().ID:
		host, found := d.pairedHost(owner)
		if !found {
			return ledger.HolderUnknown
		}
		return remoteHolderVerdict(ctx, []*pairedHost{host}, holder)
	case err == nil:
		archived, err := d.Registry.SessionArchived(holder)
		if err != nil {
			return ledger.HolderUnknown
		}
		if archived {
			return ledger.HolderArchived
		}
		return ledger.HolderLive
	case !errors.Is(err, sql.ErrNoRows):
		return ledger.HolderUnknown
	}
	return remoteHolderVerdict(ctx, candidates, holder)
}

func remoteHolderVerdict(ctx context.Context, candidates []*pairedHost, id string) string {
	unanswered := false
	for _, host := range candidates {
		if state, _ := host.status(); state != hostStateConnected {
			unanswered = true
			continue
		}
		var answer resolveResponse
		if err := host.client.GetJSON(ctx, "/api/v1/resolve", url.Values{"target": []string{id}}, &answer); err != nil {
			unanswered = true
			continue
		}
		for _, match := range answer.Matches {
			if match.ID != id {
				continue
			}
			if match.Archived {
				return ledger.HolderArchived
			}
			return ledger.HolderLive
		}
	}
	if unanswered {
		return ledger.HolderUnknown
	}
	return ledger.HolderGone
}

// takeoverScope is the caller's role's decision on taking over an item in its
// own project's ledger; the person's words, passed as an utterance, decide
// instead and are checked by the ledger.
func (d *Daemon) takeoverScope(caller *mcpCaller, itemID string) (string, error) {
	switch caller.role.Decision("work-takeover") {
	case agentroles.Allow:
		return "", nil
	case agentroles.Notify:
		return "Took over work item " + itemID + " from its holder", nil
	}
	return "", &mcpRefusal{Code: "capability_missing", Message: "Taking over " + itemID + " from its holder is for the person or your project's coordinator. Ask the person; once they say so, run it again with --utterance <their words>.",
		Detail: map[string]any{"rule": "person_only"}}
}
