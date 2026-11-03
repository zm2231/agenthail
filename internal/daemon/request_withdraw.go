package daemon

import (
	"context"
	"fmt"
	"net/http"

	"github.com/zm2231/agenthail/internal/surface"
)

// withdrawReplacedRequest settles an open request of a lead a restart
// replaced, so it stops waiting on the person: a permission prompt is denied
// and a question dismissed, both with a note saying why.
func (d *Daemon) withdrawReplacedRequest(ctx context.Context, adapter surface.Surface, session *surface.Session, requestID, actor string) (actionResult, error) {
	if requestID == "" {
		return actionResult{}, invalidRequest("requestId is required")
	}
	coord := d.coordinator()
	current, replaced := coord.ReplacedBy(session.ID)
	if !replaced {
		return actionResult{}, actionFailed(http.StatusConflict, "not_replaced", "only a request from a lead that was replaced can be withdrawn; answer it instead")
	}
	spec, _ := coord.Of(session.ID)
	note := fmt.Sprintf("Withdrawn: you are no longer coordinator @%s; %s took over. The person did not answer this request.", spec.Name, d.resolveDisplay(current))
	return d.respond(ctx, adapter, session, requestID, surface.Decision{Behavior: "deny", Message: note}, actor)
}
