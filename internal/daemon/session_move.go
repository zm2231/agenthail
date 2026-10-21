package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// moveManifest is what a source declares about a moving conversation before
// any of its files leave. The target keeps it and holds the files it then
// receives to it.
type moveManifest struct {
	ID             string              `json:"id"`
	Conversation   string              `json:"conversation"`
	SourceHost     string              `json:"sourceHost"`
	Surface        surface.SurfaceKind `json:"surface"`
	Checkout       moveCheckout        `json:"checkout"`
	Alias          string              `json:"alias,omitempty"`
	Title          string              `json:"title,omitempty"`
	Model          string              `json:"model,omitempty"`
	PermissionMode string              `json:"permissionMode,omitempty"`
	Size           int64               `json:"size"`
	SHA256         string              `json:"sha256"`
}

type moveStatus struct {
	State   string           `json:"state"`
	Detail  string           `json:"detail,omitempty"`
	Cwd     string           `json:"cwd,omitempty"`
	Session *surface.Session `json:"session,omitempty"`
}

const (
	moveNoRecord        = "none"
	moveTransferTimeout = 6 * time.Hour
)

// movesInFlight keeps one daemon goroutine at a time driving a move.
var movesInFlight sync.Map

func claimMove(id string) (func(), bool) {
	if _, busy := movesInFlight.LoadOrStore(id, true); busy {
		return nil, false
	}
	return func() { movesInFlight.Delete(id) }, true
}

type sessionMoveRequest struct {
	SessionID    string `json:"sessionId"`
	Host         string `json:"host"`
	StopTerminal bool   `json:"stopTerminal,omitempty"`
	WaitSeconds  int    `json:"waitSeconds,omitempty"`
}

type sessionMoveResult struct {
	Move    registry.SessionMove `json:"move"`
	Host    dashboardHost        `json:"host"`
	Stopped string               `json:"stopped,omitempty"`
	Alias   string               `json:"alias,omitempty"`
}

// MoveRefusedError is a move that did not happen and left the session here.
type MoveRefusedError struct{ Reason string }

func (e MoveRefusedError) Error() string { return e.Reason }

// MoveUnknownError is a move whose outcome this daemon cannot settle yet.
// The session has no writer until the target answers; recovery asks it each
// time the host reconnects.
type MoveUnknownError struct {
	MoveID string
	Err    error
}

func (e MoveUnknownError) Error() string {
	return fmt.Sprintf("move %s is unresolved: %s; it settles when the host answers", e.MoveID, e.Err)
}

func (e MoveUnknownError) Unwrap() error { return e.Err }

// moveSession moves an idle local session to a paired host. It is the only
// driver of the move: the target only answers it.
func (d *Daemon) moveSession(ctx context.Context, request sessionMoveRequest) (sessionMoveResult, error) {
	session, err := d.Registry.Session(request.SessionID)
	if err != nil {
		return sessionMoveResult{}, MoveRefusedError{Reason: fmt.Sprintf("session %s not found", request.SessionID)}
	}
	if reason, err := d.Registry.SessionMoveRefusal(session.ID); err != nil {
		return sessionMoveResult{}, err
	} else if reason != "" {
		return sessionMoveResult{}, MoveRefusedError{Reason: fmt.Sprintf("session %s cannot move: %s", session.ID, reason)}
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		return sessionMoveResult{}, err
	}
	mover, ok := adapter.(surface.SessionMover)
	if !ok {
		return sessionMoveResult{}, MoveRefusedError{Reason: fmt.Sprintf("%s sessions cannot move between hosts", session.Surface)}
	}
	host, found := d.pairedHost(request.Host)
	if !found {
		return sessionMoveResult{}, MoveRefusedError{Reason: fmt.Sprintf("no paired host %q", request.Host)}
	}
	if state, detail := host.status(); state != hostStateConnected {
		return sessionMoveResult{}, MoveRefusedError{Reason: hostUnavailableReason(host.record.Name, state, detail)}
	}
	if session.Cwd != "" {
		if _, err := sourceCheckout(ctx, session.Cwd); err != nil {
			return sessionMoveResult{}, MoveRefusedError{Reason: err.Error()}
		}
	}
	out, err := mover.PrepareMove(ctx, session, surface.AdoptRequest{StopTerminal: request.StopTerminal, Wait: time.Duration(request.WaitSeconds) * time.Second})
	if err != nil {
		return sessionMoveResult{}, err
	}
	defer out.Release()
	checkout, err := sourceCheckout(ctx, out.Cwd)
	if err != nil {
		return sessionMoveResult{}, MoveRefusedError{Reason: err.Error()}
	}
	alias, _ := d.Registry.ReverseAlias(session.ID)
	manifest := moveManifest{ID: uuid.NewString(), Conversation: session.ID, SourceHost: d.hostIdentity().ID, Surface: session.Surface, Checkout: checkout, Alias: alias, Title: out.Title, Model: out.Model, PermissionMode: out.PermissionMode, Size: out.Size, SHA256: out.SHA256}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return sessionMoveResult{}, err
	}
	release, _ := claimMove(manifest.ID)
	defer release()
	if err := d.Registry.BeginMoveOut(manifest.ID, session.ID, host.record.ID, encoded); err != nil {
		return sessionMoveResult{}, MoveRefusedError{Reason: err.Error()}
	}
	result := sessionMoveResult{Host: host.ref(), Stopped: out.Stopped, Alias: alias}
	if err := d.prepareMoveOnHost(ctx, host, manifest); err != nil {
		_ = d.Registry.SetMoveState(manifest.ID, registry.MoveFailed, err.Error(), registry.MovePreparing)
		result.Move, _ = d.Registry.SessionMove(manifest.ID)
		return result, MoveRefusedError{Reason: fmt.Sprintf("%s did not accept the move: %s", host.record.Name, err)}
	}
	if err := d.Registry.SetMoveState(manifest.ID, registry.MoveSent, "", registry.MovePreparing); err != nil {
		return result, err
	}
	err = d.settleMoveOut(ctx, host, mover, manifest.ID, session.ID)
	result.Move, _ = d.Registry.SessionMove(manifest.ID)
	return result, err
}

func (d *Daemon) prepareMoveOnHost(ctx context.Context, host *pairedHost, manifest moveManifest) error {
	body, _ := json.Marshal(manifest)
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/session-move/prepare", nil, body, nil)
	if err != nil {
		return err
	}
	_, err = hosts.ReadResponse(response)
	var status hosts.StatusError
	if errors.As(err, &status) && status.Status == http.StatusNotFound && status.Code == "" {
		return fmt.Errorf("update Agenthail on %s; it cannot take moved sessions", host.record.Name)
	}
	return err
}

// settleMoveOut drives a sent move to done or failed: it archives the
// conversation, asks the target what it holds and acts on the answer. A
// move whose answer cannot be had stays sent and returns MoveUnknownError.
func (d *Daemon) settleMoveOut(ctx context.Context, host *pairedHost, mover surface.SessionMover, moveID, conversation string) error {
	if err := mover.ArchiveMove(conversation); err != nil {
		return d.abandonMoveOut(ctx, host, mover, moveID, conversation, fmt.Errorf("archive the conversation: %w", err))
	}
	status, err := d.moveStatusOnHost(ctx, host, moveID)
	if err != nil {
		return MoveUnknownError{MoveID: moveID, Err: err}
	}
	switch status.State {
	case registry.MoveResumed:
		return d.completeMoveOut(ctx, host, moveID)
	case registry.MoveAborted:
		return d.restoreMoveOut(mover, moveID, conversation, "the host refused the move")
	case moveNoRecord:
		return d.abandonMoveOut(ctx, host, mover, moveID, conversation, fmt.Errorf("the host has no record of the move"))
	}
	status, err = d.transferMove(ctx, host, mover, moveID, conversation)
	switch {
	case err == nil && status.State == registry.MoveResumed:
		return d.completeMoveOut(ctx, host, moveID)
	case moveRefusal(err):
		return d.abandonMoveOut(ctx, host, mover, moveID, conversation, err)
	case err != nil:
		return MoveUnknownError{MoveID: moveID, Err: err}
	}
	return MoveUnknownError{MoveID: moveID, Err: fmt.Errorf("the host answered %s", status.State)}
}

// abandonMoveOut ends a move the target has not resumed: once the target
// records it as aborted, so a late copy of the request is refused, the
// conversation's files come back here. A target that cannot be told leaves
// the move unresolved.
func (d *Daemon) abandonMoveOut(ctx context.Context, host *pairedHost, mover surface.SessionMover, moveID, conversation string, cause error) error {
	query := url.Values{"id": []string{moveID}, "conversation": []string{conversation}}
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/session-move/abort", query, []byte(`{}`), nil)
	if err == nil {
		var body []byte
		body, err = hosts.ReadResponse(response)
		if err == nil {
			var status moveStatus
			if json.Unmarshal(body, &status) != nil || status.State != registry.MoveAborted {
				err = fmt.Errorf("the host answered %q to the abort", status.State)
			}
		}
	}
	var conflict hosts.StatusError
	if errors.As(err, &conflict) && conflict.Code == "move_resumed" {
		return d.completeMoveOut(ctx, host, moveID)
	}
	if errors.As(err, &conflict) && conflict.Code == "move_running" {
		return MoveUnknownError{MoveID: moveID, Err: fmt.Errorf("%v; the conversation already runs on %s, so the move finishes there", cause, host.record.Name)}
	}
	if err != nil {
		return MoveUnknownError{MoveID: moveID, Err: fmt.Errorf("%v; telling the host to drop it failed: %w", cause, err)}
	}
	if err := d.restoreMoveOut(mover, moveID, conversation, cause.Error()); err != nil {
		return err
	}
	return MoveRefusedError{Reason: fmt.Sprintf("%s did not take the session: %s", host.record.Name, cause)}
}

// moveRefusal reports whether the target refused a transfer before it could
// have started the conversation. Any other failure leaves the outcome
// unknown, and the move is never abandoned on it.
func moveRefusal(err error) bool {
	var status hosts.StatusError
	return errors.As(err, &status) && status.Status == http.StatusConflict && (status.Code == "move_refused" || status.Code == "move_aborted")
}

func (d *Daemon) restoreMoveOut(mover surface.SessionMover, moveID, conversation, detail string) error {
	if err := mover.RestoreMove(conversation); err != nil {
		return fmt.Errorf("move %s: put the conversation back: %w", moveID, err)
	}
	if err := d.Registry.SetMoveState(moveID, registry.MoveFailed, detail, registry.MoveSent, registry.MovePreparing); err != nil {
		return err
	}
	d.requestCatalogDiscovery()
	return nil
}

func (d *Daemon) completeMoveOut(ctx context.Context, host *pairedHost, moveID string) error {
	placeholder, err := d.movedPlaceholder(host, moveID)
	if err != nil {
		return err
	}
	d.catalog.mu.Lock()
	_, err = d.Registry.CompleteMoveOut(moveID, placeholder)
	if err == nil {
		d.publishCommittedHostWriteLocked("move " + moveID)
	}
	d.catalog.mu.Unlock()
	if err != nil {
		return err
	}
	if move, err := d.Registry.SessionMove(moveID); err == nil {
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: move.Conversation, Result: "moved to " + host.record.Name})
	}
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
	d.requestCatalogDiscovery()
	go d.drainHostDeliveries(context.WithoutCancel(ctx), host)
	return nil
}

// movedPlaceholder is the row a moved session shows here, as the target's,
// until the target's catalog reports it.
func (d *Daemon) movedPlaceholder(host *pairedHost, moveID string) (registry.MovePlaceholder, error) {
	move, err := d.Registry.SessionMove(moveID)
	if err != nil {
		return registry.MovePlaceholder{}, err
	}
	session, err := d.Registry.Session(move.Conversation)
	if err != nil {
		return registry.MovePlaceholder{}, err
	}
	ref := host.ref()
	reason := fmt.Sprintf("moved to %s; waiting for it to report the session", host.record.Name)
	row := dashboardSession{ID: hosts.Qualify(host.record.ID, session.ID), Surface: session.Surface, RuntimeKind: session.RuntimeKind, Name: session.Name, Status: surface.StatusOffline, Host: &ref, UnavailableReason: reason}
	encoded, err := json.Marshal(row)
	if err != nil {
		return registry.MovePlaceholder{}, err
	}
	return registry.MovePlaceholder{EntityID: row.ID, Projection: encoded, UnavailableReason: reason}, nil
}

func (d *Daemon) moveStatusOnHost(ctx context.Context, host *pairedHost, moveID string) (moveStatus, error) {
	var status moveStatus
	err := host.client.GetJSON(ctx, "/api/v1/session-move", url.Values{"id": []string{moveID}}, &status)
	return status, err
}

// transferMove streams the archived conversation to the target, which
// answers once it has resumed it.
func (d *Daemon) transferMove(ctx context.Context, host *pairedHost, mover surface.SessionMover, moveID, conversation string) (moveStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, moveTransferTimeout)
	defer cancel()
	reader, writer := io.Pipe()
	go func() { writer.CloseWithError(mover.WriteMoveArchive(writer, conversation)) }()
	response, err := host.client.Stream(ctx, "/api/v1/session-move/transfer", url.Values{"id": []string{moveID}}, reader, http.Header{"Content-Type": []string{"application/x-tar"}})
	reader.CloseWithError(errors.New("transfer ended"))
	if err != nil {
		return moveStatus{}, err
	}
	body, err := hosts.ReadResponse(response)
	if err != nil {
		return moveStatus{}, err
	}
	var status moveStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return moveStatus{}, err
	}
	return status, nil
}

// failInterruptedMoves fails every move out a previous daemon left
// preparing: none of its files left, so its conversation is untouched here.
func (d *Daemon) failInterruptedMoves() {
	moves, err := d.Registry.SessionMoves(true)
	if err != nil {
		d.log.Printf("session moves: %s", err)
		return
	}
	for _, move := range moves {
		if move.State == registry.MovePreparing {
			_ = d.Registry.SetMoveState(move.ID, registry.MoveFailed, "the daemon stopped before the move was sent", registry.MovePreparing)
		}
	}
}

// resumeHostMoves settles each sent move to a host that has just connected.
func (d *Daemon) resumeHostMoves(ctx context.Context, host *pairedHost) {
	moves, err := d.Registry.SessionMoves(true)
	if err != nil {
		d.logRuntimeError("host "+host.record.Name+" moves", err)
		return
	}
	for _, move := range moves {
		if move.PeerHost != host.record.ID || move.State != registry.MoveSent {
			continue
		}
		release, ok := claimMove(move.ID)
		if !ok {
			continue
		}
		var manifest moveManifest
		mover := surface.SessionMover(nil)
		if json.Unmarshal(move.Manifest, &manifest) == nil {
			mover, _ = d.adapters().ForSurface(manifest.Surface).(surface.SessionMover)
		}
		if mover == nil {
			release()
			d.logRuntimeError("move "+move.ID, fmt.Errorf("no adapter can move %s", move.Conversation))
			continue
		}
		err := d.settleMoveOut(ctx, host, mover, move.ID, move.Conversation)
		release()
		var refused MoveRefusedError
		if err != nil && !errors.As(err, &refused) {
			d.logRuntimeError("move "+move.ID, err)
		}
	}
}

func (d *Daemon) registerMoveAPI(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/session-moves", d.apiV1Guard(dashboard, "control", d.apiSessionMovesHandler))
	mux.HandleFunc("/api/v1/session-move", d.apiV1Guard(dashboard, "control", d.moveHostOnly(d.apiMoveStatusHandler)))
	mux.HandleFunc("/api/v1/session-move/prepare", d.apiV1Guard(dashboard, "control", d.moveHostOnly(d.apiMovePrepareHandler)))
	mux.HandleFunc("/api/v1/session-move/transfer", d.apiV1Guard(dashboard, "control", d.moveHostOnly(d.apiMoveTransferHandler)))
	mux.HandleFunc("/api/v1/session-move/abort", d.apiV1Guard(dashboard, "control", d.moveHostOnly(d.apiMoveAbortHandler)))
}

// moveHostOnly admits only a paired host's token, with control: a move in
// names its source by that token, never by what the request says.
func (d *Daemon) moveHostOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hostID, isHost := requestingHost(r.Context()); !isHost || hostID == "" {
			writeAPIError(w, http.StatusForbidden, "forbidden", "Only a paired host can move a session here.")
			return
		}
		next(w, r)
	}
}

func (d *Daemon) apiSessionMovesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		moves, err := d.Registry.SessionMoves(r.URL.Query().Get("unfinished") == "1")
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "moves_unavailable", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"moves": moves})
	case http.MethodPost:
		var request sessionMoveRequest
		if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if request.SessionID == "" || request.Host == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "sessionId and host are required")
			return
		}
		result, err := d.moveSession(context.WithoutCancel(r.Context()), request)
		var refused MoveRefusedError
		var unknown MoveUnknownError
		var busy *surface.AdoptBusyError
		var consent *surface.AdoptConsentError
		switch {
		case err == nil:
			writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result})
		case errors.As(err, &unknown):
			writeDashboardJSON(w, http.StatusAccepted, map[string]any{"ok": false, "unknown": true, "error": err.Error(), "result": result})
		case errors.As(err, &busy):
			writeDashboardJSON(w, http.StatusConflict, map[string]any{"ok": false, "busy": busy.Reason, "error": err.Error()})
		case errors.As(err, &consent):
			writeDashboardJSON(w, http.StatusConflict, map[string]any{"ok": false, "needsConsent": true, "error": err.Error()})
		case errors.As(err, &refused):
			writeDashboardJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": err.Error(), "result": result})
		default:
			writeDashboardJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error(), "result": result})
		}
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or POST for this endpoint.")
	}
}

// moveIn reads a move in this host holds for the requesting host.
func (d *Daemon) moveIn(r *http.Request) (registry.SessionMove, moveManifest, bool) {
	hostID, _ := requestingHost(r.Context())
	move, err := d.Registry.SessionMove(r.URL.Query().Get("id"))
	if err != nil || move.Role != "in" || move.PeerHost != hostID {
		return registry.SessionMove{}, moveManifest{}, false
	}
	var manifest moveManifest
	_ = json.Unmarshal(move.Manifest, &manifest)
	return move, manifest, true
}

func (d *Daemon) apiMoveStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	move, _, found := d.moveIn(r)
	if !found {
		writeDashboardJSON(w, http.StatusOK, moveStatus{State: moveNoRecord})
		return
	}
	writeDashboardJSON(w, http.StatusOK, moveStatus{State: move.State, Detail: move.Detail})
}

func validMoveID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil && strings.ToLower(id) == id
}

// apiMovePrepareHandler checks this host can take a conversation and records
// the manifest the move's files are held to. Nothing here changes yet.
func (d *Daemon) apiMovePrepareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	var manifest moveManifest
	if err := decodeAPIV1JSON(w, r, &manifest, false); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	hostID, _ := requestingHost(r.Context())
	if manifest.SourceHost != hostID {
		writeAPIError(w, http.StatusForbidden, "forbidden", "A move names its own host as its source.")
		return
	}
	if !validMoveID(manifest.ID) || !validMoveID(manifest.Conversation) || manifest.Size < 0 || len(manifest.SHA256) != 64 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The move's id, conversation, size or digest is not valid.")
		return
	}
	mover, ok := d.adapters().ForSurface(manifest.Surface).(surface.SessionMover)
	if !ok {
		writeAPIError(w, http.StatusConflict, "move_refused", fmt.Sprintf("This host cannot take %s sessions.", manifest.Surface))
		return
	}
	if owner, err := d.Registry.SessionHost(manifest.Conversation); err == nil && owner != "" && owner != hostID {
		writeAPIError(w, http.StatusConflict, "move_refused", fmt.Sprintf("Session %s belongs to another host here.", manifest.Conversation))
		return
	}
	target, err := d.resolveMoveTarget(r.Context(), manifest.ID, manifest.Checkout)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "move_refused", err.Error())
		return
	}
	if err := mover.CheckMoveIn(moveInOf(manifest, target.cwd)); err != nil {
		writeAPIError(w, http.StatusConflict, "move_refused", err.Error())
		return
	}
	encoded, _ := json.Marshal(manifest)
	move, err := d.Registry.PrepareMoveIn(manifest.ID, manifest.Conversation, hostID, encoded)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "move_refused", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, moveStatus{State: move.State, Cwd: target.cwd})
}

func moveInOf(manifest moveManifest, cwd string) surface.MoveIn {
	return surface.MoveIn{MoveID: manifest.ID, Conversation: manifest.Conversation, Cwd: cwd, Title: manifest.Title, Model: manifest.Model, PermissionMode: manifest.PermissionMode, Size: manifest.Size, SHA256: manifest.SHA256}
}

// apiMoveTransferHandler takes a prepared conversation's files from the
// stream, resumes it and makes it a session of this host. A transfer again
// for a move already resumed answers as the first did.
func (d *Daemon) apiMoveTransferHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	move, manifest, found := d.moveIn(r)
	if !found {
		writeAPIError(w, http.StatusNotFound, "move_unknown", "This host has no record of that move.")
		return
	}
	switch move.State {
	case registry.MoveResumed:
		session, _ := d.Registry.Session(move.Conversation)
		writeDashboardJSON(w, http.StatusOK, moveStatus{State: move.State, Session: session})
		return
	case registry.MoveAborted:
		writeAPIError(w, http.StatusConflict, "move_aborted", "That move was aborted.")
		return
	}
	mover, ok := d.adapters().ForSurface(manifest.Surface).(surface.SessionMover)
	if !ok {
		writeAPIError(w, http.StatusConflict, "move_refused", fmt.Sprintf("This host cannot take %s sessions.", manifest.Surface))
		return
	}
	if err := d.Registry.SetMoveState(move.ID, registry.MoveReceived, "", registry.MovePrepared, registry.MoveReceived); err != nil {
		writeAPIError(w, http.StatusConflict, "move_refused", err.Error())
		return
	}
	ctx := context.WithoutCancel(r.Context())
	target, err := d.resolveMoveTarget(ctx, move.ID, manifest.Checkout)
	if err == nil {
		err = ensureMoveCheckout(ctx, target, manifest.Checkout)
	}
	if err != nil {
		writeAPIError(w, http.StatusConflict, "move_refused", err.Error())
		return
	}
	session, err := mover.ReceiveMove(ctx, moveInOf(manifest, target.cwd), r.Body)
	if err != nil {
		if running, runErr := mover.MoveInRunning(ctx, manifest.Conversation); runErr != nil || running || surface.IsDeliveryOutcomeUnknown(err) {
			writeAPIError(w, http.StatusBadGateway, "move_unknown", err.Error())
			return
		}
		writeAPIError(w, http.StatusConflict, "move_refused", err.Error())
		return
	}
	d.catalog.mu.Lock()
	_, err = d.Registry.TakeMovedSession(move.ID, *session, manifest.Alias)
	if err == nil {
		d.publishCommittedHostWriteLocked("move " + move.ID)
	}
	d.catalog.mu.Unlock()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "move_unrecorded", fmt.Sprintf("Session %s resumed here, but its move was not recorded: %s", session.ID, err))
		return
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: "moved here from " + requestingHostName(r.Context())})
	if d.dashboard != nil {
		d.dashboard.invalidate()
	}
	d.requestCatalogDiscovery()
	writeDashboardJSON(w, http.StatusOK, moveStatus{State: registry.MoveResumed, Session: session})
}

func (d *Daemon) apiMoveAbortHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	id, conversation := r.URL.Query().Get("id"), r.URL.Query().Get("conversation")
	if !validMoveID(id) || !validMoveID(conversation) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The move's id and conversation are required.")
		return
	}
	hostID, _ := requestingHost(r.Context())
	if move, err := d.Registry.SessionMove(id); err == nil && move.Role == "in" && move.PeerHost == hostID && move.State == registry.MoveReceived {
		var manifest moveManifest
		_ = json.Unmarshal(move.Manifest, &manifest)
		mover, ok := d.adapters().ForSurface(manifest.Surface).(surface.SessionMover)
		if !ok {
			writeAPIError(w, http.StatusConflict, "move_refused", "This host cannot check that move.")
			return
		}
		if running, err := mover.MoveInRunning(r.Context(), conversation); err != nil || running {
			writeAPIError(w, http.StatusConflict, "move_running", "The moved session already runs here; transfer again to finish the move.")
			return
		}
	}
	move, err := d.Registry.AbortMoveIn(id, conversation, hostID)
	var conflict registry.MoveConflictError
	switch {
	case errors.As(err, &conflict) && conflict.State == registry.MoveResumed:
		writeAPIError(w, http.StatusConflict, "move_resumed", "That move already resumed the session here.")
	case err != nil:
		writeAPIError(w, http.StatusConflict, "move_refused", err.Error())
	default:
		writeDashboardJSON(w, http.StatusOK, moveStatus{State: move.State, Detail: move.Detail})
	}
}
