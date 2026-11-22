package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	PersonActionAPI      = "action"
	PersonActionSettings = "settings"

	approvalLifetime = 24 * time.Hour
)

// A PersonAction is the exact request a gated command sends the daemon: an
// action body for /api/v1/actions, or a settings body. Its scope is read from
// the body, never from what the caller says it touches.
type PersonAction struct {
	API  string          `json:"api"`
	Body json.RawMessage `json:"body"`
}

type personActionRequest struct {
	dashboardActionRequest
	ResumeAfterLimit bool `json:"resumeAfterLimit"`
}

func (a PersonAction) decode() (personActionRequest, error) {
	if a.API != PersonActionAPI && a.API != PersonActionSettings {
		return personActionRequest{}, refuse("invalid", "payload api is action or settings.")
	}
	var request personActionRequest
	if err := json.Unmarshal(a.Body, &request); err != nil {
		return personActionRequest{}, refuse("invalid", "payload body: "+err.Error())
	}
	if strings.TrimSpace(request.Action) == "" {
		return personActionRequest{}, refuse("invalid", "payload body names no action.")
	}
	return request, nil
}

func (d *Daemon) personActionScope(ctx context.Context, caller *mcpCaller, action PersonAction) (string, error) {
	request, err := action.decode()
	if err != nil {
		return "", err
	}
	if slices.Contains(controlApprovalActions, request.Action) {
		return "", refuse("invalid", "the action "+request.Action+" is checked as a send or control, not as the person's.")
	}
	description, err := d.describePersonAction(action.API, request)
	if err != nil {
		return "", err
	}
	if caller.has("control.all") {
		return "", nil
	}
	if d.leadActsOnOwn(ctx, caller, action.API, request) {
		switch d.agentPolicy().Decision(agentPolicyActionOf(request)) {
		case dashconfig.AgentAllow:
			return "", nil
		case dashconfig.AgentNotify:
			return d.agentNotices.hold(caller.session.ID, description, time.Now()), nil
		}
	}
	return "", &mcpRefusal{Code: "capability_missing", Message: description + " is for the person. To ask them to allow it, run the same command again with --ask-person --reason \"<why you need it>\"; their answer arrives as your next message.",
		Detail: map[string]any{"rule": "person_only", "approvable": true}}
}

func agentPolicyActionOf(request personActionRequest) string {
	if request.Action == "project-setting" && request.Setting == registry.ProjectSettingLimitResume {
		return registry.ProjectSettingLimitResume
	}
	return request.Action
}

// leadActsOnOwn reports whether a project's lead is acting on its own
// project; only then does the person's agent policy decide.
func (d *Daemon) leadActsOnOwn(ctx context.Context, caller *mcpCaller, api string, request personActionRequest) bool {
	if !caller.has("control.project") || caller.project == nil || !d.mcpIsProjectLead(caller, caller.session.ID) {
		return false
	}
	if api != PersonActionAPI {
		return false
	}
	switch request.Action {
	case "channel-create":
		return true
	case "project-add":
		if request.Host != "" || strings.TrimSpace(request.Path) == "" {
			return false
		}
		found, err := ResolvePathProject(ctx, d.Registry, request.Path)
		return err == nil && found == nil
	case "project-rename", "project-setting", "project-archive", "project-unarchive", "project-remove", "coordinator-clear":
		return d.ownProject(caller, request.ProjectID)
	case "project-link":
		return d.ownProject(caller, request.Survivor)
	case "project-unlink":
		links, _, err := d.projectLinkState()
		link, found := links[request.ProjectID]
		return err == nil && found && link.Linked && d.ownProject(caller, link.Survivor)
	case "coordinator-set":
		return request.Host == "" && d.sessionsInProject(caller, request.SessionID)
	case "coordinator-new":
		where := strings.TrimSpace(request.Cwd)
		if where == "" {
			where = strings.TrimSpace(request.Path)
		}
		if request.Host != "" || where == "" {
			return false
		}
		found, err := ResolvePathProject(ctx, d.Registry, where)
		return err == nil && found != nil && found.ID == caller.project.ID
	case "relay-add":
		return d.sessionsInProject(caller, request.FromID, request.ToID)
	case "relay-remove":
		if request.Host != "" {
			return false
		}
		from, to, found := d.relayEnds(request.RelayID)
		return found && d.sessionsInProject(caller, from, to)
	case "channel-rename", "channel-delete":
		return d.ownChannel(caller, request.Channel)
	case "channel-add", "channel-remove":
		return d.ownChannel(caller, request.Channel) && d.sessionsInProject(caller, request.TargetID)
	case "queue-clear":
		if request.Senders == nil {
			return false
		}
		ends := []string{request.SessionID}
		for _, sender := range request.Senders {
			if sender != "" {
				ends = append(ends, sender)
			}
		}
		return d.sessionsInProject(caller, ends...)
	case "queue-cancel":
		item, err := d.Registry.QueueItem(request.QueueID, d.adapters())
		if err != nil {
			return false
		}
		ends := []string{item.SessionID}
		if item.SourceSessionID != "" {
			ends = append(ends, item.SourceSessionID)
		}
		return d.sessionsInProject(caller, ends...)
	}
	return false
}

// A stored channel belongs to no project, so a lead manages one only while
// every session in it is in the lead's own project.
func (d *Daemon) ownChannel(caller *mcpCaller, channel string) bool {
	target, err := broadcast.Resolve(d.Registry, d.localChannelRef(channel))
	if err != nil || target.Channel == "" {
		return false
	}
	members, err := d.Registry.ChannelMembers(target.Channel)
	if err != nil {
		return false
	}
	return len(members) == 0 || d.sessionsInProject(caller, members...)
}

func (d *Daemon) ownProject(caller *mcpCaller, projectID string) bool {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return false
	}
	found, err := d.catalogProjectOf(projectID)
	return err == nil && found != nil && found.ID == caller.project.ID
}

func (d *Daemon) sessionsInProject(caller *mcpCaller, refs ...string) bool {
	for _, ref := range refs {
		id, err := d.Registry.ResolveTarget(strings.TrimSpace(ref))
		if err != nil {
			return false
		}
		session, err := d.Registry.Session(id)
		if err != nil || !d.sameProject(caller, *session) {
			return false
		}
	}
	return len(refs) > 0
}

func (d *Daemon) relayEnds(id int64) (string, string, bool) {
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		return "", "", false
	}
	for _, route := range routes {
		if route.ID == id {
			return route.FromSession, route.ToSession, true
		}
	}
	return "", "", false
}

// The person reads this, not the agent's words, so an agent cannot label one
// action as another. An action it cannot word cannot be asked for.
func (d *Daemon) describePersonAction(api string, request personActionRequest) (string, error) {
	if api == PersonActionSettings {
		if request.Action == "resume-after-limit" {
			if request.ResumeAfterLimit {
				return "Turn on resuming sessions a usage limit stopped", nil
			}
			return "Turn off resuming sessions a usage limit stopped", nil
		}
		return "", refuse("invalid", "the setting "+request.Action+" cannot be asked for.")
	}
	_, records, err := d.projectLinkState()
	if err != nil {
		return "", err
	}
	project := func(id string) string { return projectIDName(strings.TrimSpace(id), records) }
	session := func(ref string) string {
		ref = strings.TrimSpace(ref)
		if id, err := d.Registry.ResolveTarget(ref); err == nil {
			ref = id
		}
		return d.rowNameOr(ref, ref)
	}
	onHost := ""
	if request.Host != "" {
		onHost = " on " + request.Host
	}
	channel := "#" + strings.TrimPrefix(strings.TrimSpace(request.Channel), "#")
	switch request.Action {
	case "project-add":
		return "Add " + strings.TrimSpace(request.Path) + onHost + " as a project", nil
	case "project-remove":
		return "Remove project " + project(request.ProjectID), nil
	case "project-archive":
		return "Archive project " + project(request.ProjectID), nil
	case "project-unarchive":
		return "Unarchive project " + project(request.ProjectID), nil
	case "project-rename":
		return fmt.Sprintf("Rename project %s to %s", project(request.ProjectID), strings.TrimSpace(request.Title)), nil
	case "project-link":
		return fmt.Sprintf("Link %s into project %s", project(request.ProjectID), project(request.Survivor)), nil
	case "project-unlink":
		return "Unlink " + project(request.ProjectID) + " from the project it is linked into", nil
	case "project-setting":
		return fmt.Sprintf("Set %s to %s for project %s", request.Setting, request.Value, project(request.ProjectID)), nil
	case "coordinator-set":
		return "Make " + session(request.SessionID) + onHost + " its project's coordinator", nil
	case "coordinator-new":
		where := strings.TrimSpace(request.Cwd)
		if where == "" {
			where = strings.TrimSpace(request.Path)
		}
		return fmt.Sprintf("Start a new %s coordinator for the project at %s%s", request.Surface, where, onHost), nil
	case "coordinator-clear":
		return "Clear the coordinator of project " + project(request.ProjectID), nil
	case "relay-add":
		return fmt.Sprintf("Relay %s's replies to %s", session(request.FromID), session(request.ToID)), nil
	case "relay-remove":
		return "Remove relay #" + strconv.FormatInt(request.RelayID, 10) + onHost, nil
	case "channel-create":
		return "Create channel " + channel, nil
	case "channel-delete":
		return "Delete channel " + channel, nil
	case "channel-rename":
		return fmt.Sprintf("Rename channel %s to #%s", channel, strings.TrimPrefix(strings.TrimSpace(request.NewChannel), "#")), nil
	case "channel-add":
		return fmt.Sprintf("Add %s to %s", session(request.TargetID), channel), nil
	case "channel-remove":
		return fmt.Sprintf("Remove %s from %s", session(request.TargetID), channel), nil
	case "send":
		if request.BusyDelivery == "steer" {
			return fmt.Sprintf("Send %s this message now, steering its turn if it is busy: %q", session(request.SessionID), clipText(request.Message, 400)), nil
		}
		return fmt.Sprintf("Send %s this message: %q", session(request.SessionID), clipText(request.Message, 400)), nil
	case "steer":
		return fmt.Sprintf("Steer %s's turn with this message: %q", session(request.SessionID), clipText(request.Message, 400)), nil
	case "interrupt":
		return "Interrupt " + session(request.SessionID) + "'s turn", nil
	case "session-archive":
		return "Archive session " + session(request.SessionID), nil
	case "session-unarchive":
		return "Unarchive session " + session(request.SessionID), nil
	case "queue-cancel":
		return "Cancel queued message #" + strconv.FormatInt(request.QueueID, 10), nil
	case "queue-clear":
		if request.Senders == nil {
			return "Cancel every queued message to " + session(request.SessionID), nil
		}
		from := make([]string, 0, len(request.Senders))
		for _, sender := range request.Senders {
			if sender == "" {
				from = append(from, "no session")
			} else {
				from = append(from, session(sender))
			}
		}
		return "Cancel the queued messages to " + session(request.SessionID) + " from " + strings.Join(from, ", "), nil
	}
	return "", refuse("invalid", "the action "+request.Action+" cannot be asked for.")
}

func (d *Daemon) rowName(sessionID string) string {
	return d.rowNameOr(sessionID, "An agent")
}

func (d *Daemon) rowNameOr(sessionID, unknown string) string {
	alias, name, err := d.Registry.SessionLabel(sessionID)
	switch {
	case err == nil && alias != "":
		return "@" + alias
	case err == nil && strings.TrimSpace(name) != "":
		return strings.TrimSpace(name)
	}
	return unknown
}

func (d *Daemon) askPersonToAllow(ctx context.Context, caller *mcpCaller, action PersonAction, reason string) (registry.Ask, error) {
	request, err := action.decode()
	if err != nil {
		return registry.Ask{}, err
	}
	description, err := d.describePersonAction(action.API, request)
	if err != nil {
		return registry.Ask{}, err
	}
	var canonical any
	if err := json.Unmarshal(action.Body, &canonical); err != nil {
		return registry.Ask{}, refuse("invalid", "payload body: "+err.Error())
	}
	body, err := json.Marshal(canonical)
	if err != nil {
		return registry.Ask{}, err
	}
	reason = strings.TrimSpace(reason)
	question := description
	if reason != "" {
		question += ". Why: " + reason
	}
	now := time.Now()
	ask, raised, err := d.Registry.RaiseApproval(registry.Ask{
		Title: clipText(fmt.Sprintf("%s asks: %s", d.rowName(caller.session.ID), description), registry.MaxAskTitle),
		Questions: []registry.AskQuestion{{
			Question: clipText(question, registry.MaxAskQuestion),
			Options:  []registry.AskOption{{ID: registry.ApprovalAllow, Label: "Allow once"}, {ID: registry.ApprovalDeny, Label: "Deny"}},
		}},
	}, registry.Approval{SessionID: caller.session.ID, API: action.API, Payload: body, Description: description, Reason: reason, ExpiresAt: now.Add(approvalLifetime)}, now)
	if err != nil {
		return registry.Ask{}, err
	}
	if raised {
		d.log.Printf("approval %s: %s asks the person: %s", ask.ID, d.resolveDisplay(caller.session.ID), description)
		d.announceAsk(ask)
		if err := d.Registry.MarkAskAnnounced(ask.ID, time.Now()); err != nil {
			d.log.Printf("approval %s: mark told: %s", ask.ID, err)
		}
		d.publishAsk(ask.SessionID)
	}
	return ask, nil
}
