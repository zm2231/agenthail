package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/zm2231/agenthail/internal/registry"
)

// An action crosses at most one host boundary: back to its sender, never on.
func (d *Daemon) hostTransit(ctx context.Context, target *pairedHost) error {
	requester, fromHost := requestingHost(ctx)
	if !fromHost || target == nil || requester == target.record.ID {
		return nil
	}
	return actionFailed(http.StatusConflict, "host_transit", fmt.Sprintf("this needs host %s; a paired host's request is not passed on to another host", target.record.Name))
}

// movedFromHere refuses a paired host's request for a session this host
// moved to target, naming target, so the sender asks target itself rather
// than this host passing it on.
func (d *Daemon) movedFromHere(ctx context.Context, target *pairedHost, request map[string]any) error {
	requester, fromHost := requestingHost(ctx)
	sessionID, _ := request["sessionId"].(string)
	if !fromHost || target == nil || requester == target.record.ID || sessionID == "" {
		return nil
	}
	to, err := d.Registry.MovedTo(sessionID)
	if err != nil || to != target.record.ID {
		return err
	}
	return d.movedRefusal(sessionID, target.record.ID, target.record.Name)
}

// The count stops a replayed older signed handover.
func (d *Daemon) movedRefusal(sessionID, hostID, hostName string) error {
	seq, err := d.Registry.HandoverSeq(sessionID)
	if err != nil {
		return err
	}
	return &actionError{Status: http.StatusConflict, Code: movedCode, Message: fmt.Sprintf("session %s moved to host %s; send it there", sessionID, hostName), Extra: map[string]any{movedToHostField: hostID, handoverSeqField: seq}}
}

func (d *Daemon) coordinatorMovedRefusal(name, hostID string) error {
	hostName := hostID
	if host, found := d.pairedHost(hostID); found {
		hostName = host.record.Name
	}
	return &actionError{Status: http.StatusConflict, Code: movedCode, Message: fmt.Sprintf("coordinator @%s moved to host %s; send it there", name, hostName), Extra: map[string]any{movedToHostField: hostID}}
}

// sessionMovedError is the moved refusal for a session a paired host now
// serves, or nil.
func sessionMovedError(err error) error {
	var owned registry.SessionOwnedError
	if errors.As(err, &owned) && owned.HostID != "" {
		return registry.MovedError(owned)
	}
	return nil
}

func (d *Daemon) movedAnswer(err error) error {
	if owned := sessionMovedError(err); owned != nil {
		err = owned
	}
	var moved registry.MovedError
	if !errors.As(err, &moved) {
		return err
	}
	name := moved.HostID
	if host, found := d.pairedHost(moved.HostID); found {
		name = host.record.Name
	}
	return d.movedRefusal(moved.SessionID, moved.HostID, name)
}

const (
	movedCode        = "moved"
	movedToHostField = "movedToHost"
	handoverSeqField = "handoverSeq"
)

func movedTo(answer hostAnswer) (string, uint64) {
	if answer.status != http.StatusConflict {
		return "", 0
	}
	var refusal struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		MovedToHost string `json:"movedToHost"`
		HandoverSeq uint64 `json:"handoverSeq"`
	}
	if json.Unmarshal(answer.payload, &refusal) != nil || refusal.Error.Code != movedCode {
		return "", 0
	}
	return refusal.MovedToHost, refusal.HandoverSeq
}

func movedToHost(answer hostAnswer) string {
	to, _ := movedTo(answer)
	return to
}
