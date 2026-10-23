package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/hosts"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) coordinator() coordinator.Service {
	return coordinator.Service{Registry: d.Registry, Adapters: d.adapters(), Dispatcher: d.dispatcher()}
}

// restartCoordinator delivers a queued message by starting the coordinator
// again when its conversation is gone. It reports whether it handled item.
func (d *Daemon) restartCoordinator(ctx context.Context, session *surface.Session, item *registry.QueuedMessage, now time.Time) bool {
	coord := d.coordinator()
	if current, replaced := coord.ReplacedBy(session.ID); replaced {
		if err := d.Registry.MoveClaimedMessage(item.ID, current); err != nil {
			d.log.Printf("move queue item %d to coordinator %s: %s", item.ID, current, err)
			if _, err := d.Registry.DeferMessage(item.ID, err, now); err != nil {
				d.log.Printf("defer queue item %d: %s", item.ID, err)
			}
		}
		return true
	}
	composed, _ := item.Uploads.Compose(item.Message, false)
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	outcome, handled, err := coord.Restart(operationCtx, session, composed)
	if !handled {
		return false
	}
	if err != nil {
		d.finishQueueFailure(item, session, err, now)
		return true
	}
	receipt := outcome.Receipt
	if err := d.Registry.AckMessageWithEvidence(item.ID, session.ID, item.RelayHops, receipt.Evidence, receipt.TurnID); err != nil {
		d.log.Printf("ack queue item %d: %s", item.ID, err)
		return true
	}
	d.log.Printf("delivered queue item %d by starting coordinator %s: %s", item.ID, outcome.Session.ID, outcome.Reason)
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "delivered", SessionID: outcome.Session.ID, SourceSessionID: item.SourceSessionID, QueueID: item.ID, Message: item.Message, Result: receipt.TurnID, Evidence: receipt.Evidence})
	return true
}

type coordinatorRequest struct {
	Action          string
	SessionID       string
	SourceSessionID string
	ProjectID       string
	Name            string
	Surface         string
	Cwd             string
	Path            string
	Model           string
	PermissionMode  string
	Instructions    *string
	Replace         bool
	ChangedAt       time.Time
	ChangedBy       string
	Message         string
	Author          surface.Author
	PersonName      string
}

// coordinatorAction sets or clears a coordinator on the host that owns it.
// A set runs where its conversation lives, which the action router picks
// from the session or the host the request names.
func (d *Daemon) coordinatorAction(r *http.Request, w http.ResponseWriter, request coordinatorRequest) {
	ctx := r.Context()
	switch request.Action {
	case "coordinator-set":
		session, err := d.Registry.Session(request.SessionID)
		if err != nil {
			writeAPIError(w, http.StatusNotFound, "session_not_found", fmt.Sprintf("no session %s on this host", request.SessionID))
			return
		}
		if owner, err := d.Registry.SessionHost(session.ID); err != nil || owner != "" || session.Cwd == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("%s has no working directory on this host to start again in", session.ID))
			return
		}
		project, ok := d.coordinatorProject(ctx, w, session.Cwd)
		if !ok {
			return
		}
		spec, err := d.coordinatorSpec(request, project, string(session.Surface), session.Cwd, session.ConfiguredModel)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		d.assignCoordinator(r, w, request, spec, session.ID)
	case "coordinator-new":
		kind := surface.SurfaceKind(request.Surface)
		if kind != surface.KindClaude && kind != surface.KindCodex {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("a coordinator is a claude or codex conversation, not %q", request.Surface))
			return
		}
		if _, ok := d.adapters().ForSurface(kind).(surface.SessionStarter); !ok {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("%s is not configured to start conversations on %s", kind, d.hostIdentity().Name))
			return
		}
		folder := request.Cwd
		if folder == "" {
			folder = request.Path
		}
		if !filepath.IsAbs(folder) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "a coordinator needs an absolute folder on its host")
			return
		}
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("%s is not a folder on %s", folder, d.hostIdentity().Name))
			return
		}
		project, ok := d.coordinatorProject(ctx, w, folder)
		if !ok {
			return
		}
		cwd := request.Cwd
		if cwd == "" {
			cwd = project.RootPath
		}
		spec, err := d.coordinatorSpec(request, project, string(kind), cwd, request.Model)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		d.assignCoordinator(r, w, request, spec, "")
	case "coordinator-clear":
		d.clearCoordinator(r, w, request)
	case "coordinator-send":
		d.sendToCoordinator(r, w, request)
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "unknown coordinator action")
	}
}

// sendToCoordinator delivers to this host's coordinator by name, starting
// its conversation when it has none, for a sender that cannot name a session
// because the coordinator may not have one yet.
func (d *Daemon) sendToCoordinator(r *http.Request, w http.ResponseWriter, request coordinatorRequest) {
	name := strings.TrimPrefix(strings.TrimSpace(request.Name), "@")
	if name == "" || strings.TrimSpace(request.Message) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "a coordinator name and a message are required")
		return
	}
	spec, err := d.Registry.CoordinatorNamed(name)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "no_coordinator", fmt.Sprintf("%s has no coordinator @%s", d.hostIdentity().Name, name))
		return
	}
	if request.Author != "" && request.Author != surface.AuthorPerson || request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "author must be \"person\" and name no source session when set")
		return
	}
	if request.SourceSessionID != "" {
		if _, err := d.Registry.Session(request.SourceSessionID); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "source session not found")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	outcome, err := d.coordinator().Deliver(ctx, spec, request.Message, surface.SendOptions{Model: request.Model, SourceSessionID: request.SourceSessionID, Author: request.Author, PersonName: request.PersonName})
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "delivery_failed", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": outcome.Receipt, "started": outcome.Started})
}

func (d *Daemon) coordinatorProject(ctx context.Context, w http.ResponseWriter, folder string) (*CatalogProject, bool) {
	project, err := ResolvePathProject(ctx, d.Registry, folder)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("find the project of %s: %s", folder, err))
		return nil, false
	}
	if project == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("%s is not in a project on %s; use a Git repository or run 'agenthail project add <folder>'", folder, d.hostIdentity().Name))
		return nil, false
	}
	return project, true
}

func (d *Daemon) coordinatorSpec(request coordinatorRequest, project *CatalogProject, agent, cwd, model string) (registry.CoordinatorSpec, error) {
	name := strings.TrimPrefix(strings.TrimSpace(request.Name), "@")
	if name == "" {
		name = DefaultCoordinatorName(project.DisplayName)
	}
	if strings.ContainsAny(name, " \t\r\n/#:") || len(name) > 80 {
		return registry.CoordinatorSpec{}, fmt.Errorf("name must be 1 to 80 characters without spaces, /, :, or #")
	}
	spec := registry.CoordinatorSpec{ProjectID: project.ID, ProjectRoot: project.RootPath, Name: name, Surface: agent, Cwd: cwd, Model: model, PermissionMode: request.PermissionMode}
	if spec.PermissionMode != "" && agent != string(surface.KindClaude) {
		return spec, fmt.Errorf("--permission-mode applies to a claude coordinator only")
	}
	if request.Instructions != nil {
		spec.Instructions = *request.Instructions
	} else if previous, err := d.Registry.Coordinator(project.ID); err == nil {
		spec.Instructions = previous.Instructions
	}
	return spec, nil
}

var coordinatorNameCharacters = regexp.MustCompile(`[^a-z0-9._-]+`)

// DefaultCoordinatorName is <project>-lead, from the project's display name.
func DefaultCoordinatorName(displayName string) string {
	const suffix = "-lead"
	base := strings.Trim(coordinatorNameCharacters.ReplaceAllString(strings.ToLower(displayName), "-"), "-.")
	if len(base) > 80-len(suffix) {
		base = strings.TrimRight(base[:80-len(suffix)], "-.")
	}
	if base == "" {
		base = "project"
	}
	return base + suffix
}

// coordinatorCaller names who changes a coordinator: the session that ran
// the command, qualified with its host, else the person or device acting.
// A paired host names its own caller.
func (d *Daemon) coordinatorCaller(r *http.Request, request coordinatorRequest) string {
	if _, fromHost := requestingHost(r.Context()); fromHost && strings.TrimSpace(request.ChangedBy) != "" {
		return strings.TrimSpace(request.ChangedBy)
	}
	if request.SourceSessionID != "" {
		if public := d.publicSessionID(request.SourceSessionID); public != request.SourceSessionID {
			return public
		}
		return hosts.Qualify(d.hostIdentity().ID, request.SourceSessionID)
	}
	return actionActor(r)
}

// coordinatorRef is how the caller names a coordinator: @name on its own
// host, host::@name on any other.
func (d *Daemon) coordinatorRef(r *http.Request, spec registry.CoordinatorSpec) string {
	if spec.HostID == "" {
		if _, fromHost := requestingHost(r.Context()); fromHost {
			return d.hostIdentity().Name + "::@" + spec.Name
		}
		return "@" + spec.Name
	}
	if requester, fromHost := requestingHost(r.Context()); fromHost && requester == spec.HostID {
		return "@" + spec.Name
	}
	name := spec.HostID
	if host, found := d.pairedHost(spec.HostID); found {
		name = host.record.Name
	}
	return name + "::@" + spec.Name
}

func (d *Daemon) coordinatorRefs(r *http.Request, specs []registry.CoordinatorSpec) string {
	refs := make([]string, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, d.coordinatorRef(r, spec))
	}
	return strings.Join(refs, " and ")
}

// assignCoordinator makes spec this host's coordinator of its project. A
// project with a coordinator on any host is refused unless the request
// replaces it; a paired host's coordinator is cleared by that host first,
// and nothing changes here when it cannot confirm.
func (d *Daemon) assignCoordinator(r *http.Request, w http.ResponseWriter, request coordinatorRequest, spec registry.CoordinatorSpec, sessionID string) {
	d.coordinatorMu.Lock()
	defer d.coordinatorMu.Unlock()
	changedBy := d.coordinatorCaller(r, request)
	existing, err := d.Registry.ProjectCoordinators(spec.ProjectID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "registry_error", err.Error())
		return
	}
	if len(existing) == 1 && sameCoordinator(existing[0], spec, sessionID) {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "coordinator": existing[0], "replaced": []registry.CoordinatorSpec{}})
		return
	}
	if len(existing) > 0 && !request.Replace {
		writeAPIError(w, http.StatusConflict, "coordinator_exists", fmt.Sprintf("%s already has coordinator %s; replace it to take over", spec.ProjectRoot, d.coordinatorRefs(r, existing)))
		return
	}
	change := registry.CoordinatorChange{SessionID: sessionID, Replace: request.Replace, OverPeers: true, ChangedBy: changedBy}
	stored, undo, err := d.Registry.ReplaceCoordinator(spec, change)
	if err != nil {
		writeCoordinatorError(w, r, d, err)
		return
	}
	d.flushCatalog("coordinator " + spec.ProjectID)
	cleared := 0
	for _, current := range existing {
		if current.HostID == "" {
			continue
		}
		if err := d.clearPeerCoordinator(r.Context(), current, changedBy); err != nil {
			message := fmt.Sprintf("cannot replace %s: %s", d.coordinatorRef(r, current), err)
			if cleared > 0 {
				writeAPIError(w, http.StatusConflict, "coordinator_conflict", message+fmt.Sprintf("; @%s now coordinates the project here alongside it, and replacing again resolves it", stored.Name))
				return
			}
			undoErr := d.Registry.WithdrawCoordinator(undo, changedBy)
			d.flushCatalog("coordinator " + spec.ProjectID)
			if undoErr != nil {
				writeAPIError(w, http.StatusConflict, "coordinator_conflict", message+fmt.Sprintf("; @%s stays here alongside it because it could not be withdrawn (%s), and replacing again resolves it", stored.Name, undoErr))
				return
			}
			writeAPIError(w, http.StatusConflict, "coordinator_not_replaced", message+"; nothing changed")
			return
		}
		cleared++
	}
	d.flushCatalog("coordinator " + spec.ProjectID)
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "coordinator": stored, "replaced": existing})
}

func writeCoordinatorError(w http.ResponseWriter, r *http.Request, d *Daemon, err error) {
	var exists registry.CoordinatorExistsError
	var nameTaken registry.CoordinatorNameTakenError
	var aliasTaken registry.AliasTakenError
	var aliasReserved registry.AliasReservedError
	switch {
	case errors.As(err, &exists):
		writeAPIError(w, http.StatusConflict, "coordinator_exists", fmt.Sprintf("the project already has coordinator %s; replace it to take over", d.coordinatorRefs(r, exists.Existing)))
	case errors.As(err, &nameTaken):
		writeAPIError(w, http.StatusConflict, "name_taken", fmt.Sprintf("%s already coordinates %s; choose another name", d.coordinatorRef(r, registry.CoordinatorSpec{Name: nameTaken.Name, HostID: nameTaken.HostID}), nameTaken.ProjectRoot))
	case errors.As(err, &aliasTaken):
		writeAPIError(w, http.StatusConflict, "name_taken", fmt.Sprintf("@%s already names session %s; choose another name", aliasTaken.Name, aliasTaken.Owner))
	case errors.As(err, &aliasReserved):
		writeAPIError(w, http.StatusConflict, "name_taken", err.Error())
	case errors.Is(err, registry.ErrReservedAlias):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, registry.ErrNoCoordinator):
		writeAPIError(w, http.StatusNotFound, "no_coordinator", err.Error())
	case errors.Is(err, registry.ErrCoordinatorChanged):
		writeAPIError(w, http.StatusConflict, "coordinator_changed", err.Error())
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
	}
}

// clearCoordinator clears this host's coordinator of a project, or asks the
// paired host that owns it to. A host answering a paired host clears only
// its own.
func (d *Daemon) clearCoordinator(r *http.Request, w http.ResponseWriter, request coordinatorRequest) {
	if request.ProjectID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "projectId is required")
		return
	}
	if _, fromHost := requestingHost(r.Context()); fromHost {
		if !d.coordinatorMu.TryLock() {
			writeAPIError(w, http.StatusConflict, "coordinator_busy", fmt.Sprintf("%s is changing a coordinator; try again", d.hostIdentity().Name))
			return
		}
	} else {
		d.coordinatorMu.Lock()
	}
	defer d.coordinatorMu.Unlock()
	changedBy := d.coordinatorCaller(r, request)
	existing, err := d.Registry.ProjectCoordinators(request.ProjectID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "registry_error", err.Error())
		return
	}
	var remote []registry.CoordinatorSpec
	for _, current := range existing {
		if current.HostID != "" {
			remote = append(remote, current)
			continue
		}
		cleared, err := d.Registry.ClearCoordinator(request.ProjectID, changedBy, request.ChangedAt)
		if err != nil {
			writeCoordinatorError(w, r, d, err)
			return
		}
		d.flushCatalog("coordinator " + request.ProjectID)
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": cleared})
		return
	}
	if _, fromHost := requestingHost(r.Context()); fromHost {
		writeCoordinatorError(w, r, d, registry.ErrNoCoordinator)
		return
	}
	if len(remote) == 0 {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": nil})
		return
	}
	if len(remote) > 1 {
		writeAPIError(w, http.StatusConflict, "coordinator_conflict", fmt.Sprintf("the project has coordinators %s; clear it on one of those hosts", d.coordinatorRefs(r, remote)))
		return
	}
	if err := d.clearPeerCoordinator(r.Context(), remote[0], changedBy); err != nil {
		writeAPIError(w, http.StatusConflict, "coordinator_not_cleared", fmt.Sprintf("cannot clear %s: %s", d.coordinatorRef(r, remote[0]), err))
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "cleared": remote[0]})
}

// sameCoordinator reports whether current, this host's coordinator, is what
// spec asks for, so setting it again changes nothing. A conversation made
// the coordinator must be the same one; a coordinator defined to start on
// its first message must start the same way.
func sameCoordinator(current, spec registry.CoordinatorSpec, sessionID string) bool {
	if current.HostID != "" || current.Name != spec.Name || current.Surface != spec.Surface || current.PermissionMode != spec.PermissionMode || current.Instructions != spec.Instructions {
		return false
	}
	if sessionID != "" {
		return current.SessionID == sessionID
	}
	return current.Cwd == spec.Cwd && current.Model == spec.Model
}

// clearPeerCoordinator asks the paired host that owns a coordinator to clear
// it, provided it has not changed since this host mirrored it, then drops it
// from the mirror.
func (d *Daemon) clearPeerCoordinator(ctx context.Context, current registry.CoordinatorSpec, changedBy string) error {
	host, found := d.pairedHost(current.HostID)
	if !found {
		return fmt.Errorf("its host %s is no longer paired", current.HostID)
	}
	if state, detail := host.status(); state == hostStateIncompatible {
		return fmt.Errorf("host %s needs an update: %s", host.record.Name, detail)
	}
	projectID := current.ProjectID
	if _, raw, qualified := hosts.Split(projectID); qualified {
		projectID = raw
	}
	body, err := json.Marshal(map[string]any{"action": "coordinator-clear", "projectId": projectID, "changedAt": current.ChangedAt, "changedBy": changedBy})
	if err != nil {
		return err
	}
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/actions", nil, body, http.Header{actionIdempotencyHeader: []string{"coordinator-" + uuid.NewString()}})
	if err != nil {
		return fmt.Errorf("host %s is unreachable: %w", host.record.Name, err)
	}
	if _, err := hosts.ReadResponse(response); err != nil {
		return fmt.Errorf("host %s refused: %w", host.record.Name, err)
	}
	return d.Registry.RemoveHostCoordinator(current.HostID, current.ProjectID)
}

func (d *Daemon) flushCatalog(what string) {
	d.catalog.mu.Lock()
	defer d.catalog.mu.Unlock()
	d.publishCommittedHostWriteLocked(what)
}
