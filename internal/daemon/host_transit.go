package daemon

import (
	"context"
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
