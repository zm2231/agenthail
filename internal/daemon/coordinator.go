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
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) coordinator() coordinator.Service {
	return coordinator.Service{Registry: d.Registry, Adapters: d.adapters(), Dispatcher: d.dispatcher(), ClaudeRoot: func(previous string) (string, error) {
		pick, err := d.placeClaudeSession("", previous)
		return pick.Root, err
	}, LeadsChanged: d.syncLeads, Replaced: d.announceReplacementsSoon, Now: d.coordinatorNow}
}

// restartCoordinator delivers a queued message by starting the coordinator
// again when its conversation is gone. It reports whether it handled item.
func (d *Daemon) restartCoordinator(ctx context.Context, session *surface.Session, item *registry.QueuedMessage, now time.Time) bool {
	coord := d.coordinator()
	route, err := coord.RouteOf(ctx, session.ID, coordinator.FromAgent(item.Author, item.SourceSessionID))
	if err != nil {
		if err := d.Registry.DeferMessage(item.ID, err, now); err != nil {
			d.log.Printf("defer queue item %d: %s", item.ID, err)
		}
		return true
	}
	if route.Replaced && !route.Moved {
		return false
	}
	if route.Moved {
		if err := d.Registry.MoveClaimedMessage(item.ID, route.Current); err != nil {
			d.log.Printf("move queue item %d to coordinator %s: %s", item.ID, route.Current, err)
			if err := d.Registry.DeferMessage(item.ID, err, now); err != nil {
				d.log.Printf("defer queue item %d: %s", item.ID, err)
			}
		}
		return true
	}
	composed, _ := item.Uploads.Compose(item.Message, false)
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	if messageUUID, err := d.Registry.DeliveryIntentUUIDForQueue(item.ID); err != nil {
		d.log.Printf("read delivery intent of queue item %d: %s", item.ID, err)
	} else if messageUUID != "" {
		operationCtx = surface.WithMessageUUID(operationCtx, messageUUID)
	}
	if item.SourceSessionID != "" {
		operationCtx = surface.WithSourceSessionID(operationCtx, item.SourceSessionID)
	}
	outcome, handled, err := coord.Restart(operationCtx, session, composed)
	if !handled {
		return false
	}
	if err != nil {
		d.finishQueueFailure(item, session, err, now, registry.SendTrace{})
		return true
	}
	receipt := outcome.Receipt
	if err := d.Registry.AckRestartedMessage(item.ID, session.ID, outcome.Session.ID, item.RelayHops, receipt.Evidence, receipt.TurnID); err != nil {
		d.log.Printf("ack queue item %d: %s", item.ID, err)
		return true
	}
	if err := d.catalog.flushCommitted(); err != nil {
		d.log.Printf("publish restarted queue item %d: %s", item.ID, err)
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
	Project         string
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
	TurnOptions     surface.TurnOptions
	NoQueue         bool
}

// coordinatorAction sets or clears a coordinator on the host that owns it.
// A set runs where its conversation lives, which the action router picks
// from the session or the host the request names.
func (d *Daemon) doCoordinatorAction(ctx context.Context, p principal, request coordinatorRequest) (actionResult, error) {
	if request.Action != "coordinator-send" && request.Action != "coordinator-brief" {
		defer d.syncLeads()
	}
	switch request.Action {
	case "coordinator-set":
		session, err := d.Registry.Session(request.SessionID)
		if err != nil {
			return actionResult{}, actionFailed(http.StatusNotFound, "session_not_found", fmt.Sprintf("no session %s on this host", request.SessionID))
		}
		if owner, err := d.Registry.SessionHost(session.ID); err != nil || owner != "" || session.Cwd == "" {
			return actionResult{}, invalidRequest(fmt.Sprintf("%s has no working directory on this host to start again in", session.ID))
		}
		project, err := d.coordinatorProject(ctx, session.Cwd)
		if err != nil {
			return actionResult{}, err
		}
		spec, err := d.coordinatorSpec(request, project, string(session.Surface), session.Cwd, session.ConfiguredModel)
		if err != nil {
			return actionResult{}, invalidRequest(err.Error())
		}
		return d.assignCoordinator(ctx, p, request, spec, session.ID)
	case "coordinator-new":
		kind := surface.SurfaceKind(request.Surface)
		if kind != surface.KindClaude && kind != surface.KindCodex {
			return actionResult{}, invalidRequest(fmt.Sprintf("a coordinator is a claude or codex conversation, not %q", request.Surface))
		}
		if _, ok := d.adapters().ForSurface(kind).(surface.SessionStarter); !ok {
			return actionResult{}, invalidRequest(fmt.Sprintf("%s is not configured to start conversations on %s", kind, d.hostIdentity().Name))
		}
		folder := request.Cwd
		if folder == "" {
			folder = request.Path
		}
		if !filepath.IsAbs(folder) {
			return actionResult{}, invalidRequest("a coordinator needs an absolute folder on its host")
		}
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			return actionResult{}, invalidRequest(fmt.Sprintf("%s is not a folder on %s", folder, d.hostIdentity().Name))
		}
		project, err := d.coordinatorProject(ctx, folder)
		if err != nil {
			return actionResult{}, err
		}
		cwd := request.Cwd
		if cwd == "" {
			cwd = project.RootPath
		}
		spec, err := d.coordinatorSpec(request, project, string(kind), cwd, request.Model)
		if err != nil {
			return actionResult{}, invalidRequest(err.Error())
		}
		return d.assignCoordinator(ctx, p, request, spec, "")
	case "coordinator-clear":
		return d.clearCoordinator(ctx, p, request)
	case "coordinator-send", "coordinator-brief":
		return d.sendToCoordinator(ctx, request)
	default:
		return actionResult{}, invalidRequest("unknown coordinator action")
	}
}

type coordinatorSendReply struct {
	OK      bool              `json:"ok"`
	Ref     string            `json:"ref"`
	Result  *delivery.Receipt `json:"result"`
	Started bool              `json:"started"`
	Since   *briefSince       `json:"since,omitempty"`
}

// sendToCoordinator delivers to a coordinator named directly on this host, or
// to a project's lead on this host or a paired one, starting its
// conversation when it has none.
func (d *Daemon) sendToCoordinator(ctx context.Context, request coordinatorRequest) (actionResult, error) {
	name := strings.TrimPrefix(strings.TrimSpace(request.Name), "@")
	project := strings.TrimSpace(request.Project)
	if name != "" && project != "" {
		return actionResult{}, invalidRequest("name a coordinator or a project, not both")
	}
	brief := request.Action == "coordinator-brief"
	if brief && strings.TrimSpace(request.Message) != "" {
		return actionResult{}, invalidRequest("a brief takes no message; the lead is asked to catch the person up")
	}
	if name == "" && project == "" || !brief && strings.TrimSpace(request.Message) == "" {
		return actionResult{}, invalidRequest("a coordinator name or a project, and a message, are required")
	}
	var spec registry.CoordinatorSpec
	var err error
	if name != "" {
		if spec, err = d.Registry.CoordinatorNamed(name); err != nil {
			if to, movedErr := d.Registry.CoordinatorMovedTo(name); movedErr == nil && to != "" {
				return actionResult{}, d.coordinatorMovedRefusal(name, to)
			}
			return actionResult{}, actionFailed(http.StatusNotFound, "no_coordinator", fmt.Sprintf("%s has no coordinator @%s", d.hostIdentity().Name, name))
		}
	} else if spec, err = d.projectLead(project); err != nil {
		return actionResult{}, actionFailed(http.StatusConflict, "no_lead", err.Error())
	}
	if request.Author != "" && request.Author != surface.AuthorPerson || request.Author == surface.AuthorPerson && request.SourceSessionID != "" {
		return actionResult{}, invalidRequest("author must be \"person\" and name no source session when set")
	}
	if err := d.knownSource(request.SourceSessionID); err != nil {
		return actionResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	options := surface.SendOptions{Model: request.Model, SourceSessionID: request.SourceSessionID, Author: request.Author, PersonName: request.PersonName, TurnOptions: request.TurnOptions}
	reply := coordinatorSendReply{OK: true, Ref: d.coordinator().Ref(spec)}
	if brief && spec.HostID == "" {
		if request.Message, reply.Since, err = d.briefRequest(spec, request.SourceSessionID); err != nil {
			return actionResult{}, err
		}
	}
	if spec.HostID != "" {
		if request.NoQueue {
			return actionResult{}, invalidRequest(fmt.Sprintf("--no-queue is not available for %s: the daemon holds a send to another host until that host answers", reply.Ref))
		}
		var peer peerCoordinatorAnswer
		peer, err = d.sendToPeerCoordinator(ctx, spec, request.Action, request.Message, options)
		reply.Result, reply.Started, reply.Since = peer.Result, peer.Started, peer.Since
	} else {
		deliver := d.coordinator().Deliver
		if request.NoQueue {
			deliver = d.coordinator().DeliverWithoutQueue
		}
		var outcome *coordinator.Outcome
		if outcome, err = deliver(ctx, spec, request.Message, options); err == nil {
			reply.Result, reply.Started = outcome.Receipt, outcome.Started
		}
	}
	var refused *actionError
	if errors.As(err, &refused) {
		return actionResult{}, err
	}
	if err != nil && spec.HostID == "" {
		if to, movedErr := d.Registry.CoordinatorMovedTo(spec.Name); movedErr == nil && to != "" {
			return actionResult{}, d.coordinatorMovedRefusal(spec.Name, to)
		}
	}
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "delivery_failed", err.Error())
	}
	return actionOK(reply), nil
}

func (d *Daemon) coordinatorProject(ctx context.Context, folder string) (*CatalogProject, error) {
	project, err := ResolvePathProject(ctx, d.Registry, folder)
	if err != nil {
		return nil, invalidRequest(fmt.Sprintf("find the project of %s: %s", folder, err))
	}
	if project == nil {
		return nil, invalidRequest(fmt.Sprintf("%s is not in a project on %s; use a Git repository or run 'agenthail project add <folder>'", folder, d.hostIdentity().Name))
	}
	return project, nil
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
func (d *Daemon) coordinatorCaller(ctx context.Context, p principal, request coordinatorRequest) string {
	if _, fromHost := requestingHost(ctx); fromHost && strings.TrimSpace(request.ChangedBy) != "" {
		return strings.TrimSpace(request.ChangedBy)
	}
	if request.SourceSessionID != "" {
		if public := d.publicSessionID(request.SourceSessionID); public != request.SourceSessionID {
			return public
		}
		return hosts.Qualify(d.hostIdentity().ID, request.SourceSessionID)
	}
	return p.actor()
}

// coordinatorRef is how the caller names a coordinator: @name on its own
// host, host::@name on any other.
func (d *Daemon) coordinatorRef(ctx context.Context, spec registry.CoordinatorSpec) string {
	if spec.HostID == "" {
		if _, fromHost := requestingHost(ctx); fromHost {
			return d.hostIdentity().Name + "::@" + spec.Name
		}
		return "@" + spec.Name
	}
	if requester, fromHost := requestingHost(ctx); fromHost && requester == spec.HostID {
		return "@" + spec.Name
	}
	name := spec.HostID
	if host, found := d.pairedHost(spec.HostID); found {
		name = host.record.Name
	}
	return name + "::@" + spec.Name
}

func (d *Daemon) coordinatorRefs(ctx context.Context, specs []registry.CoordinatorSpec) string {
	refs := make([]string, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, d.coordinatorRef(ctx, spec))
	}
	return strings.Join(refs, " and ")
}

// assignCoordinator makes spec this host's coordinator of its project. A
// project with a coordinator on any host is refused unless the request
// replaces it; a paired host's coordinator is cleared by that host first,
// and nothing changes here when it cannot confirm.
func (d *Daemon) assignCoordinator(ctx context.Context, p principal, request coordinatorRequest, spec registry.CoordinatorSpec, sessionID string) (actionResult, error) {
	d.coordinatorMu.Lock()
	defer d.coordinatorMu.Unlock()
	changedBy := d.coordinatorCaller(ctx, p, request)
	existing, err := d.Registry.ProjectCoordinators(spec.ProjectID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if len(existing) == 1 && sameCoordinator(existing[0], spec, sessionID) {
		return actionOK(map[string]any{"ok": true, "coordinator": existing[0], "replaced": []registry.CoordinatorSpec{}}), nil
	}
	if len(existing) > 0 && !request.Replace {
		return actionResult{}, actionFailed(http.StatusConflict, "coordinator_exists", fmt.Sprintf("%s already has coordinator %s; replace it to take over", spec.ProjectRoot, d.coordinatorRefs(ctx, existing)))
	}
	for _, current := range existing {
		if host, found := d.pairedHost(current.HostID); current.HostID != "" && found {
			if err := d.hostTransit(ctx, host); err != nil {
				return actionResult{}, err
			}
		}
	}
	change := registry.CoordinatorChange{SessionID: sessionID, Replace: request.Replace, OverPeers: true, ChangedBy: changedBy}
	stored, undo, err := d.Registry.ReplaceCoordinator(spec, change)
	if err != nil {
		return actionResult{}, d.coordinatorError(ctx, err)
	}
	d.flushCatalog("coordinator " + spec.ProjectID)
	cleared := 0
	for _, current := range existing {
		if current.HostID == "" {
			continue
		}
		if err := d.clearPeerCoordinator(ctx, current, changedBy); err != nil {
			message := fmt.Sprintf("cannot replace %s: %s", d.coordinatorRef(ctx, current), err)
			if cleared > 0 {
				return actionResult{}, actionFailed(http.StatusConflict, "coordinator_conflict", message+fmt.Sprintf("; @%s now coordinates the project here alongside it, and replacing again resolves it", stored.Name))
			}
			undoErr := d.Registry.WithdrawCoordinator(undo, changedBy)
			d.flushCatalog("coordinator " + spec.ProjectID)
			if undoErr != nil {
				return actionResult{}, actionFailed(http.StatusConflict, "coordinator_conflict", message+fmt.Sprintf("; @%s stays here alongside it because it could not be withdrawn (%s), and replacing again resolves it", stored.Name, undoErr))
			}
			return actionResult{}, actionFailed(http.StatusConflict, "coordinator_not_replaced", message+"; nothing changed")
		}
		cleared++
	}
	for _, current := range existing {
		previous, known := d.leadSession(current)
		if !known || sessionID == "" || previous == sessionID {
			continue
		}
		if err := d.Registry.MoveRelayRoutes(previous, sessionID); err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", fmt.Sprintf("@%s now coordinates the project, but the relays of %s stay on it: %s", stored.Name, d.resolveDisplay(previous), err))
		}
		d.publishEvent("state.changed", sessionID, map[string]string{"source": "relay"})
	}
	for _, current := range existing {
		if current.HostID != "" {
			d.handOverPeerRelays(ctx, current, sessionID)
		}
	}
	d.flushCatalog("coordinator " + spec.ProjectID)
	return actionOK(map[string]any{"ok": true, "coordinator": stored, "replaced": existing}), nil
}

func (d *Daemon) coordinatorError(ctx context.Context, err error) error {
	var exists registry.CoordinatorExistsError
	var nameTaken registry.CoordinatorNameTakenError
	var aliasTaken registry.AliasTakenError
	var aliasReserved registry.AliasReservedError
	switch {
	case errors.As(err, &exists):
		return actionFailed(http.StatusConflict, "coordinator_exists", fmt.Sprintf("the project already has coordinator %s; replace it to take over", d.coordinatorRefs(ctx, exists.Existing)))
	case errors.As(err, &nameTaken):
		return actionFailed(http.StatusConflict, "name_taken", fmt.Sprintf("%s already coordinates %s; choose another name", d.coordinatorRef(ctx, registry.CoordinatorSpec{Name: nameTaken.Name, HostID: nameTaken.HostID}), nameTaken.ProjectRoot))
	case errors.As(err, &aliasTaken):
		return actionFailed(http.StatusConflict, "name_taken", fmt.Sprintf("@%s already names session %s; choose another name", aliasTaken.Name, aliasTaken.Owner))
	case errors.As(err, &aliasReserved):
		return actionFailed(http.StatusConflict, "name_taken", err.Error())
	case errors.Is(err, registry.ErrReservedAlias):
		return invalidRequest(err.Error())
	case errors.Is(err, registry.ErrNoCoordinator):
		return actionFailed(http.StatusNotFound, "no_coordinator", err.Error())
	case errors.Is(err, registry.ErrCoordinatorChanged):
		return actionFailed(http.StatusConflict, "coordinator_changed", err.Error())
	default:
		return invalidRequest(err.Error())
	}
}

// clearCoordinator clears this host's coordinator of a project, or asks the
// paired host that owns it to. A host answering a paired host clears only
// its own.
func (d *Daemon) clearCoordinator(ctx context.Context, p principal, request coordinatorRequest) (actionResult, error) {
	if request.ProjectID == "" {
		return actionResult{}, invalidRequest("projectId is required")
	}
	_, fromHost := requestingHost(ctx)
	if fromHost {
		if !d.coordinatorMu.TryLock() {
			return actionResult{}, actionFailed(http.StatusConflict, "coordinator_busy", fmt.Sprintf("%s is changing a coordinator; try again", d.hostIdentity().Name))
		}
	} else {
		d.coordinatorMu.Lock()
	}
	defer d.coordinatorMu.Unlock()
	changedBy := d.coordinatorCaller(ctx, p, request)
	existing, err := d.Registry.ProjectCoordinators(request.ProjectID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	var remote []registry.CoordinatorSpec
	for _, current := range existing {
		if current.HostID != "" {
			remote = append(remote, current)
			continue
		}
		cleared, err := d.Registry.ClearCoordinator(request.ProjectID, changedBy, request.ChangedAt)
		if err != nil {
			return actionResult{}, d.coordinatorError(ctx, err)
		}
		d.flushCatalog("coordinator " + request.ProjectID)
		return actionOK(map[string]any{"ok": true, "cleared": cleared}), nil
	}
	if fromHost {
		return actionResult{}, d.coordinatorError(ctx, registry.ErrNoCoordinator)
	}
	if len(remote) == 0 {
		return actionOK(map[string]any{"ok": true, "cleared": nil}), nil
	}
	if len(remote) > 1 {
		return actionResult{}, actionFailed(http.StatusConflict, "coordinator_conflict", fmt.Sprintf("the project has coordinators %s; clear it on one of those hosts", d.coordinatorRefs(ctx, remote)))
	}
	if err := d.clearPeerCoordinator(ctx, remote[0], changedBy); err != nil {
		return actionResult{}, actionFailed(http.StatusConflict, "coordinator_not_cleared", fmt.Sprintf("cannot clear %s: %s", d.coordinatorRef(ctx, remote[0]), err))
	}
	return actionOK(map[string]any{"ok": true, "cleared": remote[0]}), nil
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

// leadSession is the session a coordinator's relays are stored against on
// this host: its own conversation, or the mirrored conversation of a paired
// host's coordinator. known is false when this host has no such session.
func (d *Daemon) leadSession(spec registry.CoordinatorSpec) (string, bool) {
	if spec.SessionID == "" {
		return "", false
	}
	if spec.HostID == "" {
		return spec.SessionID, true
	}
	_, id, _ := hosts.Split(spec.SessionID)
	owner, err := d.Registry.SessionHost(id)
	return id, err == nil && owner == spec.HostID
}

func (d *Daemon) flushCatalog(what string) {
	d.catalog.mu.Lock()
	defer d.catalog.mu.Unlock()
	d.publishCommittedHostWriteLocked(what)
}
