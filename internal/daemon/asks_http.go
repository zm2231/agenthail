package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type askView struct {
	ID         string                 `json:"id"`
	SessionID  string                 `json:"sessionId"`
	Title      string                 `json:"title"`
	Questions  []registry.AskQuestion `json:"questions"`
	RoutedTo   string                 `json:"routedTo"`
	Route      []registry.AskHop      `json:"route"`
	State      string                 `json:"state"`
	Answer     *registry.AskAnswer    `json:"answer,omitempty"`
	AnsweredBy string                 `json:"answeredBy,omitempty"`
	AnsweredAt time.Time              `json:"answeredAt,omitzero"`
	CreatedAt  time.Time              `json:"createdAt"`
}

func newAskView(ask registry.Ask) askView {
	view := askView{ID: ask.ID, SessionID: ask.SessionID, Title: ask.Title, Questions: ask.Questions, RoutedTo: ask.RoutedTo, Route: ask.Route, State: ask.State, AnsweredBy: ask.AnsweredBy, AnsweredAt: ask.AnsweredAt, CreatedAt: ask.CreatedAt}
	if ask.State == registry.AskAnswered {
		answer := ask.Answer
		view.Answer = &answer
	}
	return view
}

type askHTTPRequest struct {
	Op        string             `json:"op"`
	SessionID string             `json:"sessionId"`
	AskID     string             `json:"askId"`
	Title     string             `json:"title"`
	Questions []askQuestionInput `json:"questions"`
	Answers   map[string]string  `json:"answers"`
	Choices   []string           `json:"choices"`
	Note      string             `json:"note"`
}

// asksHandler serves the CLI on this machine, which speaks for the session it runs in; nothing else may claim a session here.
func (d *Daemon) asksHandler(w http.ResponseWriter, r *http.Request) {
	if !dashboardBearer(r.Context()) {
		writeAPIError(w, http.StatusForbidden, "local_only", "Only the agenthail command on this machine raises and answers asks here.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		d.listAsks(w, r)
	case http.MethodPost:
		var request askHTTPRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, apiRequestBodyLimit)).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_json", "The ask is not valid JSON.")
			return
		}
		d.changeAsk(r.Context(), w, request)
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or POST for this endpoint.")
	}
}

func (d *Daemon) listAsks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var asks []registry.Ask
	var err error
	if id := query.Get("id"); id != "" {
		ask, found, readErr := d.Registry.Ask(id)
		if found {
			asks = []registry.Ask{ask}
		}
		err = readErr
	} else {
		asks, err = d.Registry.OpenAsks()
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "registry_error", err.Error())
		return
	}
	routedTo := query.Get("routedTo")
	views := []askView{}
	for _, ask := range asks {
		if routedTo == "" || ask.RoutedTo == routedTo || ask.SessionID == routedTo {
			views = append(views, newAskView(ask))
		}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"asks": views})
}

func (d *Daemon) changeAsk(ctx context.Context, w http.ResponseWriter, request askHTTPRequest) {
	switch request.Op {
	case "raise":
		session, err := d.Registry.Session(request.SessionID)
		if err != nil {
			writeAPIError(w, http.StatusNotFound, "not_found", "No session "+request.SessionID+" raises asks here.")
			return
		}
		ask, err := d.raiseAsk(*session, askInput{Title: request.Title, Questions: request.Questions})
		var invalid askInvalid
		switch {
		case errors.As(err, &invalid):
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error()+".")
		case err != nil:
			writeAPIError(w, http.StatusInternalServerError, "registry_error", err.Error())
		default:
			writeDashboardJSON(w, http.StatusOK, newAskView(ask))
		}
	case "withdraw":
		session, err := d.Registry.Session(request.SessionID)
		if err != nil {
			writeAPIError(w, http.StatusNotFound, "not_found", errAskNotFound.Error()+".")
			return
		}
		switch err := d.withdrawOwnAsk(*session, request.AskID); {
		case errors.Is(err, errAskNotFound):
			writeAPIError(w, http.StatusNotFound, "not_found", err.Error()+".")
		case errors.Is(err, errAskSettled):
			writeAPIError(w, http.StatusConflict, "stale", err.Error()+".")
		case err != nil:
			writeAPIError(w, http.StatusInternalServerError, "registry_error", err.Error())
		default:
			writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "askId": request.AskID, "outcome": "withdrawn"})
		}
	case "answer", "escalate":
		d.answerAskLocally(ctx, w, request)
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "op is raise, withdraw, answer or escalate.")
	}
}

func (d *Daemon) answerAskLocally(ctx context.Context, w http.ResponseWriter, request askHTTPRequest) {
	ask, found, err := d.Registry.Ask(request.AskID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "registry_error", err.Error())
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "not_found", "No ask "+request.AskID+".")
		return
	}
	session, err := d.Registry.Session(ask.SessionID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "The session that asked is gone.")
		return
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "conflict", "surface is not configured")
		return
	}
	p := principal{ID: localDashboardPrincipal, Kind: principalPerson, Claimed: CLIActor, DashboardBearer: true}
	if by := strings.TrimSpace(request.SessionID); by != "" {
		p = sessionPrincipal(by)
	}
	response := surface.Response{Choices: request.Choices, Answers: request.Answers, Message: request.Note}
	if request.Op == "escalate" {
		response = surface.Response{Choices: []string{askEscalate}}
	}
	personName, _ := d.Registry.PersonName()
	result, err := d.answerAsk(ctx, adapter, session, ask.ID, response, p, "", personName)
	writeActionResult(w, result, err)
}
