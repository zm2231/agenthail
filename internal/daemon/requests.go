package daemon

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// requestsHandler reads a session's open requests and permission mode from
// its provider now, and the ask of an ended turn waiting on the person, none
// when nothing is open. Answers go through the respond action.
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
	asks, err := d.Registry.SessionAsks(session.ID)
	if err != nil {
		return nil, "", actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	var open []surface.Request
	for _, ask := range asks {
		if ask.State == registry.AskOpen && ask.RoutedTo == registry.AskToPerson {
			open = append(open, askRequest(ask))
		}
	}
	asking := len(open) > 0
	var state surface.RequestState
	if responder, ok := sessionResponder(adapter, session); ok {
		ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		defer cancel()
		if state, err = responder.Requests(ctx, session); err != nil {
			return nil, "", actionFailed(http.StatusBadGateway, "bad_gateway", err.Error())
		}
	} else if !asking {
		return nil, "", actionFailed(http.StatusBadRequest, "invalid_request", "this session's requests are not answered through Agenthail")
	}
	replacedBy, _ := d.coordinator().ReplacedBy(session.ID)
	requests := make([]openRequestView, 0, len(state.Requests)+len(open))
	for _, request := range state.Requests {
		requests = append(requests, openRequestView{Request: request, Prompt: request.Prompt(), Actions: request.Actions(), ReplacedBy: d.publicSessionID(replacedBy)})
	}
	for _, ask := range open {
		requests = append(requests, openRequestView{Request: ask, Prompt: ask.Prompt(), Actions: ask.Actions()})
	}
	return requests, state.PermissionMode, nil
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

// respond answers one open request and records who answered it. The history
// entry is written in the background, since the registry may be busy and the
// answer has already been delivered. A request that was already answered or
// has expired is a conflict, never a retry.
// Either way the turn is observed again and the session's catalog row
// published before the reply, so a client reading right after sees it no
// longer waiting on the request.
func (d *Daemon) respond(ctx context.Context, adapter surface.Surface, session *surface.Session, requestID string, decision surface.Decision, actor string) (actionResult, error) {
	responder, ok := sessionResponder(adapter, session)
	if !ok {
		return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", "this session's requests are not answered through Agenthail")
	}
	if decision.Choice != "" {
		state, err := responder.Requests(ctx, session)
		if err != nil {
			return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", err.Error())
		}
		request, open := openRequest(state, requestID)
		if !open {
			d.observeAfterAnswer(adapter, session)
			return actionResult{}, staleRequest()
		}
		if decision, err = request.Choose(decision.Choice, decision.Message); err != nil {
			return actionResult{}, actionFailed(http.StatusBadRequest, "invalid_request", err.Error())
		}
	}
	outcome, err := responder.Respond(ctx, session, requestID, decision)
	if errors.Is(err, surface.ErrRequestStale) {
		d.observeAfterAnswer(adapter, session)
		return actionResult{}, staleRequest()
	}
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "bad_gateway", err.Error())
	}
	entry := registry.HistoryEntry{Kind: "request-" + outcome, SessionID: session.ID, Message: requestID, Result: "answered by " + actor}
	record := func(context.Context) {
		if err := d.Registry.RecordHistory(entry); err != nil {
			d.log.Printf("record the answer to %s: %s", d.resolveDisplay(session.ID), err)
		}
	}
	if !d.background.start(record) {
		record(ctx)
	}
	d.observeAfterAnswer(adapter, session)
	return actionOK(map[string]any{"ok": true, "result": map[string]string{"requestId": requestID, "outcome": outcome}}), nil
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
