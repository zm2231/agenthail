package daemon

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// requestsHandler reads a session's open requests and permission mode from
// its provider now, each under the id of its ask, with the agent asks routed
// to the person. Answers go through the respond action.
func (d *Daemon) requestsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	session, err := d.Registry.Session(r.URL.Query().Get("sessionId"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	requests, mode, err := d.openRequests(r.Context(), session)
	if err != nil {
		writeActionResult(w, actionResult{}, err)
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"sessionId": session.ID, "permissionMode": mode, "requests": requests})
}

func (d *Daemon) openRequests(ctx context.Context, session *surface.Session) ([]openRequestView, string, error) {
	adapter, err := d.adapters().For(session)
	if err != nil {
		return nil, "", actionFailed(http.StatusConflict, "conflict", "surface is not configured")
	}
	responder, answers := sessionResponder(adapter, session)
	var state surface.RequestState
	if answers {
		ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		defer cancel()
		if state, err = responder.Requests(ctx, session); err != nil {
			return nil, "", actionFailed(http.StatusBadGateway, "bad_gateway", err.Error())
		}
	}
	asks, err := d.Registry.SessionAsks(session.ID)
	if err != nil {
		return nil, "", actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if answers && !asksCover(asks, state.Requests) {
		if _, err := d.observeTurn(ctx, adapter, session); err != nil {
			d.log.Printf("observe %s for its requests: %s", d.resolveDisplay(session.ID), err)
		}
		if asks, err = d.Registry.SessionAsks(session.ID); err != nil {
			return nil, "", actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
		}
	}
	permission := map[string]string{}
	var asking []surface.Request
	for _, ask := range asks {
		switch {
		case ask.State != registry.AskOpen:
		case ask.Origin == registry.AskOriginPermission && ask.Answerable:
			permission[ask.Ref] = ask.ID
		case (ask.Origin == registry.AskOriginAgent || ask.Origin == registry.AskOriginDelivery) && ask.RoutedTo == registry.AskToPerson:
			asking = append(asking, askRequest(ask))
		}
	}
	if !answers && len(asking) == 0 {
		return nil, "", actionFailed(http.StatusBadRequest, "invalid_request", "this session's requests are not answered through Agenthail")
	}
	replacedBy, _ := d.coordinator().ReplacedBy(session.ID)
	requests := make([]openRequestView, 0, len(state.Requests)+len(asking))
	for _, request := range state.Requests {
		id, asked := permission[request.ID]
		if !asked {
			continue
		}
		request.ID = id
		request = request.WithOptionIDs()
		requests = append(requests, openRequestView{Request: request, Prompt: request.Prompt(), Actions: request.Actions(), ReplacedBy: d.publicSessionID(replacedBy)})
	}
	for _, ask := range asking {
		requests = append(requests, openRequestView{Request: ask, Prompt: ask.Prompt(), Actions: ask.Actions()})
	}
	return requests, state.PermissionMode, nil
}

// asksCover reports whether every request the provider lists has its ask; one
// it opened since the last observation has none until it is observed.
func asksCover(asks []registry.Ask, requests []surface.Request) bool {
	refs := map[string]bool{}
	for _, ask := range asks {
		if ask.Origin == registry.AskOriginPermission && ask.State != registry.AskWithdrawn {
			refs[ask.Ref] = true
		}
	}
	for _, request := range requests {
		if !refs[request.ID] {
			return false
		}
	}
	return true
}

// openRequestView is a request with the line and buttons a notification shows
// for it, so a phone builds its notification from this Mac rather than from
// the push.
type openRequestView struct {
	surface.Request
	Prompt  string                  `json:"prompt"`
	Actions []surface.RequestAction `json:"actions,omitempty"`
	// ReplacedBy is the project's current lead when this request is from a lead a restart replaced.
	ReplacedBy string `json:"replacedBy,omitempty"`
}

// sessionResponder is the provider that answers a session's requests through
// Agenthail. Whether one is open right now does not matter: a session with
// none lists none, and an answer to one no longer open is stale.
func sessionResponder(adapter surface.Surface, session *surface.Session) (surface.Responder, bool) {
	responder, ok := adapter.(surface.Responder)
	return responder, ok && !surface.IsReadOnlySession(session)
}

// observeAfterAnswer records what the session waits on now and publishes its
// catalog row, which clears the request that is no longer open from both.
// The row is published even when this observation changed nothing: the
// observation that the answer itself started may have cleared the record
// first, and its publish may not have landed yet.
func (d *Daemon) observeAfterAnswer(adapter surface.Surface, session *surface.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	observation, err := d.observeTurn(ctx, adapter, session)
	if err != nil {
		d.log.Printf("observe %s after an answer: %s", d.resolveDisplay(session.ID), err)
		return
	}
	if observation == nil {
		return
	}
	d.publishWaiting(ctx, session.ID, observation.WaitingOn)
}

// openRequest finds a request that is still open. A notification button's
// choice is resolved against it as it is now; one no longer open is stale like
// any other late answer.
func openRequest(state surface.RequestState, requestID string) (surface.Request, bool) {
	for _, request := range state.Requests {
		if request.ID == requestID {
			return request, true
		}
	}
	return surface.Request{}, false
}

func staleRequest() error {
	return actionFailed(http.StatusConflict, "stale", surface.ErrRequestStale.Error())
}

// recordSetting writes a model or permission-mode change into the session's
// history with who made it.
func (d *Daemon) recordSetting(session *surface.Session, action, value, actor string) {
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: action, SessionID: session.ID, Message: value, Result: "changed by " + actor})
}

// ActionActorHeader carries who started an action when the daemon is not
// the first hop: the CLI through its local daemon, or a person on another
// host through that host's daemon.
const ActionActorHeader = "Agenthail-Actor"

// CLIActor is how history names an action taken from the agenthail CLI.
const CLIActor = "the CLI"

const maxActorLength = 160

// claimedActor keeps a forwarded actor to one bounded printable line.
func claimedActor(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxActorLength || !utf8.ValidString(value) {
		return ""
	}
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return ""
		}
	}
	return value
}
