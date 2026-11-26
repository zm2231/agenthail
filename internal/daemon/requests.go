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
	open, err := d.readOpenRequests(r.Context(), session, true, r.URL.Query().Get("reader"))
	if err != nil {
		writeActionResult(w, actionResult{}, err)
		return
	}
	body := map[string]any{"sessionId": session.ID, "permissionMode": open.permissionMode, "requests": open.requests}
	if open.providerError != "" {
		body["providerError"] = open.providerError
	}
	writeDashboardJSON(w, http.StatusOK, body)
}

type openRequests struct {
	requests       []openRequestView
	permissionMode string
	providerError  string
}

// A provider that cannot be read leaves its own requests out, never the asks
// Agenthail holds: an approval or a delivery problem shows while its session
// is offline.
func (d *Daemon) openRequests(ctx context.Context, session *surface.Session) (openRequests, error) {
	return d.readOpenRequests(ctx, session, true, "")
}

// readOpenRequests reads them without first recording a request the
// provider opened since its last observation unless observe is set; such a
// request is left out until it is observed. Besides the asks routed to the
// person they hold those routed to reader, a session here or, by the id its
// own host gives it, on a paired host.
func (d *Daemon) readOpenRequests(ctx context.Context, session *surface.Session, observe bool, reader string) (openRequests, error) {
	adapter, err := d.adapters().For(session)
	if err != nil {
		return openRequests{}, actionFailed(http.StatusConflict, "conflict", "surface is not configured")
	}
	responder, answers := sessionResponder(adapter, session)
	var state surface.RequestState
	providerError := ""
	if answers {
		ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		defer cancel()
		if state, err = responder.Requests(ctx, session); err != nil {
			providerError, state = err.Error(), surface.RequestState{}
		}
	}
	asks, err := d.Registry.SessionAsks(session.ID)
	if err != nil {
		return openRequests{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if observe && answers && providerError == "" && !asksCover(asks, state.Requests) {
		if _, err := d.observeTurn(ctx, adapter, session); err != nil {
			d.log.Printf("observe %s for its requests: %s", d.resolveDisplay(session.ID), err)
		}
		if asks, err = d.Registry.SessionAsks(session.ID); err != nil {
			return openRequests{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
		}
	}
	permission := map[string]string{}
	var asking []registry.Ask
	for _, ask := range asks {
		switch {
		case ask.State != registry.AskOpen:
		case ask.Origin == registry.AskOriginPermission && ask.Answerable:
			permission[ask.Ref] = ask.ID
		case ask.Origin != registry.AskOriginPermission && (ask.RoutedTo == registry.AskToPerson || d.routedTo(ask, reader)):
			asking = append(asking, ask)
		}
	}
	if !answers && len(asking) == 0 {
		return openRequests{}, actionFailed(http.StatusBadRequest, "invalid_request", "this session's requests are not answered through Agenthail")
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
		requests = append(requests, viewOpenRequest(request, d.publicSessionID(replacedBy)))
	}
	for _, ask := range asking {
		view := viewOpenRequest(askRequest(ask), "")
		view.Ask = newRequestAsk(ask)
		requests = append(requests, view)
	}
	return openRequests{requests: requests, permissionMode: state.PermissionMode, providerError: providerError}, nil
}

// viewOpenRequest sends a request's detail up to mcpDetailLimit. Its one-line
// summary goes only with a detail sent whole, so a client never offers to
// approve a request whose full detail it cannot show.
func viewOpenRequest(request surface.Request, replacedBy string) openRequestView {
	detail := request.Detail()
	view := openRequestView{Request: request, Prompt: request.Prompt(), Brief: clipUTF8(detail, mcpDetailLimit), Actions: request.Actions(), ReplacedBy: replacedBy}
	if view.Brief == detail {
		view.Summary = request.Summary()
	}
	return view
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
	Brief   string                  `json:"detail,omitempty"`
	Summary string                  `json:"summary,omitempty"`
	Actions []surface.RequestAction `json:"actions,omitempty"`
	// ReplacedBy is the project's current lead when this request is from a lead a restart replaced.
	ReplacedBy string      `json:"replacedBy,omitempty"`
	Ask        *requestAsk `json:"ask,omitempty"`
}

type requestAsk struct {
	Title     string                 `json:"title"`
	Questions []registry.AskQuestion `json:"questions"`
	DefaultAt time.Time              `json:"defaultAt,omitzero"`
	WorkItem  string                 `json:"workItem,omitempty"`
}

func newRequestAsk(ask registry.Ask) *requestAsk {
	view := &requestAsk{Title: ask.Title, Questions: ask.Questions, WorkItem: ask.WorkItem}
	if _, defaulted := askDefault(ask); defaulted {
		view.DefaultAt = ask.Deadline
	}
	return view
}

func (d *Daemon) routedTo(ask registry.Ask, reader string) bool {
	return reader != "" && ask.RoutedTo != registry.AskToPerson && d.hostQualified(ask.RoutedTo) == d.hostQualified(reader)
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

// settledRequest refuses an answer to an ask that is no longer open. For a
// failed delivery it also says how the ask was settled, so a client holding
// the message as not sent can tell a deleted or dismissed one, which never
// reached the session, from one that is being sent again.
func settledRequest(ask registry.Ask) error {
	if ask.Origin != registry.AskOriginDelivery {
		return staleRequest()
	}
	failure := &actionError{Status: http.StatusConflict, Code: "stale", Message: surface.ErrRequestStale.Error()}
	if ask.State == registry.AskWithdrawn {
		failure.Extra = map[string]any{"settled": registry.AskWithdrawn}
		return failure
	}
	if len(ask.Questions) == 0 {
		return failure
	}
	question := ask.Questions[0]
	for _, option := range question.Options {
		if ask.Answer.Answers[question.Question] == option.Label {
			failure.Extra = map[string]any{"settled": option.ID}
		}
	}
	return failure
}

// recordSetting writes a model or permission-mode change into the session's
// history with who made it.
func (d *Daemon) recordSetting(session *surface.Session, action, value, actor string) {
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: action, SessionID: session.ID, Message: value, Result: "changed by " + actor})
}

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
