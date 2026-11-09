package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	return &actionError{Status: http.StatusConflict, Code: movedCode, Message: fmt.Sprintf("session %s moved to host %s; send it there", sessionID, target.record.Name), Extra: map[string]any{movedToHostField: target.record.ID}}
}

const (
	movedCode        = "moved"
	movedToHostField = "movedToHost"
)

// movedTo is the host a paired host's answer says the session moved to, when
// that answer is a moved refusal.
func movedTo(answer hostAnswer) string {
	if answer.status != http.StatusConflict {
		return ""
	}
	var refusal struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		MovedToHost string `json:"movedToHost"`
	}
	if json.Unmarshal(answer.payload, &refusal) != nil || refusal.Error.Code != movedCode {
		return ""
	}
	return refusal.MovedToHost
}
