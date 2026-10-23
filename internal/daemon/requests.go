package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// requestsHandler reads a session's open requests and permission mode from
// its provider now, none when nothing is open. Answers go through the respond
// action.
func (d *Daemon) requestsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, err := d.Registry.Session(r.URL.Query().Get("sessionId"))
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		http.Error(w, "surface is not configured", http.StatusConflict)
		return
	}
	responder, ok := sessionResponder(adapter, session)
	if !ok {
		http.Error(w, "this session's requests are not answered through Agenthail", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	state, err := responder.Requests(ctx, session)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	requests := make([]openRequestView, 0, len(state.Requests))
	for _, request := range state.Requests {
		requests = append(requests, openRequestView{Request: request, Prompt: request.Prompt(), Actions: request.Actions()})
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"sessionId": session.ID, "permissionMode": state.PermissionMode, "requests": requests})
}

// openRequestView is a request with the line and buttons a notification shows
// for it, so a phone builds its notification from this Mac rather than from
// the push.
type openRequestView struct {
	surface.Request
	Prompt  string                  `json:"prompt"`
	Actions []surface.RequestAction `json:"actions,omitempty"`
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
func (d *Daemon) respond(ctx context.Context, w http.ResponseWriter, adapter surface.Surface, session *surface.Session, requestID string, decision surface.Decision, actor string) {
	responder, ok := sessionResponder(adapter, session)
	if !ok {
		http.Error(w, "this session's requests are not answered through Agenthail", http.StatusBadRequest)
		return
	}
	if decision.Choice != "" {
		state, err := responder.Requests(ctx, session)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		request, open := openRequest(state, requestID)
		if !open {
			d.observeAfterAnswer(adapter, session)
			writeStaleRequest(w)
			return
		}
		if decision, err = request.Choose(decision.Choice, decision.Message); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	outcome, err := responder.Respond(ctx, session, requestID, decision)
	if errors.Is(err, surface.ErrRequestStale) {
		d.observeAfterAnswer(adapter, session)
		writeStaleRequest(w)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
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
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": map[string]string{"requestId": requestID, "outcome": outcome}})
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

func writeStaleRequest(w http.ResponseWriter) {
	writeDashboardJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": map[string]string{"code": "stale", "message": surface.ErrRequestStale.Error()}})
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

// actionActor names who sent a request: a paired device by name, the person a
// paired host acted for, the CLI, or the dashboard. A claimed actor is taken
// only from a paired host, which names its own caller, or from the local
// dashboard token, which only the CLI presents with this header.
func actionActor(r *http.Request) string {
	claimed := claimedActor(r.Header.Get(ActionActorHeader))
	if principal, ok := r.Context().Value(apiHostPrincipalContextKey{}).(apiHostPrincipal); ok && strings.TrimSpace(principal.hostName) != "" {
		if !principal.host {
			return fmt.Sprintf("device %s", principal.hostName)
		}
		if claimed != "" {
			return fmt.Sprintf("%s via host %s", claimed, principal.hostName)
		}
		return fmt.Sprintf("host %s", principal.hostName)
	}
	if claimed == CLIActor && dashboardBearer(r.Context()) {
		return CLIActor
	}
	return "the dashboard"
}

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
