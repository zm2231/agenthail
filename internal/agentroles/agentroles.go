// Package agentroles is what an agent may do that used to be the person's
// alone: its reach, a decision for each agent action, the agenthail tools it
// sees and its limits.
package agentroles

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/agenttools"
)

// A decision says what happens when an agent takes an action: it runs, it
// runs and the person is told on their devices, or the agent must ask the
// person first.
const (
	Allow  = "allow"
	Notify = "notify"
	Ask    = "ask"
)

// A role's reach is which sessions it acts on beyond its own: the ones it
// started, every one in its project, or every one.
const (
	ReachDescendants = "descendants"
	ReachProject     = "project"
	ReachAll         = "all"
)

const (
	Worker   = "worker"
	Lead     = "lead"
	Operator = "operator"
	Remote   = "remote"
)

const (
	DefaultDepth   = 3
	DefaultRunning = 8
)

const LedgersAll = "all"

// Role holds only the decisions that differ from each action's default for
// the role's reach. Tools null means every agenthail tool, and an empty list
// none; a limit of 0 is the default. Ledgers names the projects whose ledgers
// the role reads and writes besides its own, by project id or LedgersAll;
// null is the default for its reach, every project for reach all and none
// for the others.
type Role struct {
	Description string            `json:"description,omitempty"`
	Reach       string            `json:"reach"`
	Actions     map[string]string `json:"actions,omitempty"`
	Tools       []string          `json:"tools"`
	Ledgers     []string          `json:"ledgers"`
	Depth       int               `json:"depth,omitempty"`
	Running     int               `json:"running,omitempty"`
}

type Action struct {
	Name    string `json:"name"`
	Default string `json:"default"`
	Detail  string `json:"detail"`
	// OwnProject marks an action on the caller's own project, which a role
	// without project reach asks for whatever its default.
	OwnProject bool `json:"ownProject"`
	// AllReach is the default for a role that reaches every session, when it
	// differs from Default.
	AllReach string `json:"allReach,omitempty"`
}

func forPerson(name, detail string) Action {
	return Action{Name: name, Default: Ask, Detail: detail, AllReach: Allow}
}

// A new action is one more row here. Actions outside this list, such as
// approving extensions or changing roles, stay the person's.
var Actions = []Action{
	{Name: "session-create-remote", Default: Notify, Detail: "Start a session on a paired computer"},
	{Name: "extension-build", Default: Notify, Detail: "Start an extension builder", AllReach: Allow},
	{Name: "project-add", Default: Allow, Detail: "Add a folder on this computer that is in no project", OwnProject: true},
	{Name: "project-rename", Default: Allow, Detail: "Rename its own project", OwnProject: true},
	{Name: "project-link", Default: Allow, Detail: "Link a project into its own", OwnProject: true},
	{Name: "project-unlink", Default: Allow, Detail: "Unlink a project from its own", OwnProject: true},
	{Name: "project-setting", Default: Allow, Detail: "Turn its own project's tools on or off", OwnProject: true},
	{Name: "project-archive", Default: Notify, Detail: "Archive its own project", OwnProject: true},
	{Name: "project-unarchive", Default: Allow, Detail: "Unarchive its own project", OwnProject: true},
	{Name: "project-remove", Default: Notify, Detail: "Remove its own project from Agenthail; the files stay", OwnProject: true},
	{Name: "coordinator-set", Default: Allow, Detail: "Make a session in its own project the lead", OwnProject: true},
	{Name: "coordinator-new", Default: Allow, Detail: "Start a new lead for its own project", OwnProject: true},
	{Name: "coordinator-clear", Default: Notify, Detail: "Clear its own project's lead", OwnProject: true},
	{Name: "relay-add", Default: Allow, Detail: "Relay replies between sessions in its own project", OwnProject: true},
	{Name: "relay-remove", Default: Allow, Detail: "Remove such a relay", OwnProject: true},
	{Name: "queue-cancel", Default: Allow, Detail: "Cancel a queued message in its own project", OwnProject: true},
	{Name: "queue-clear", Default: Allow, Detail: "Clear queued messages in its own project", OwnProject: true},
	{Name: "channel-create", Default: Allow, Detail: "Create a channel", OwnProject: true},
	{Name: "channel-rename", Default: Allow, Detail: "Rename a channel whose sessions are all in its project", OwnProject: true},
	{Name: "channel-add", Default: Allow, Detail: "Add a session in its project to a channel whose sessions are all in its project", OwnProject: true},
	{Name: "channel-remove", Default: Allow, Detail: "Remove a session from a channel whose sessions are all in its project", OwnProject: true},
	{Name: "channel-delete", Default: Notify, Detail: "Delete a channel whose sessions are all in its project", OwnProject: true},
	{Name: "work-takeover", Default: Notify, Detail: "Take over a work item in its own project whose holder is archived or gone: release it, submit it, or mark it done once its PR merged", OwnProject: true, AllReach: Allow},
	{Name: "limit-resume", Default: Notify, Detail: "Turn resuming its own project's sessions a usage limit stopped on or off", OwnProject: true},
	{Name: "overseen-control", Default: Allow, Detail: "Send now to, steer or interrupt a session it started or leads, on any computer, within its ceiling"},
	{Name: "remote-project-control", Default: Notify, Detail: "Steer, interrupt or change a session in its own project on a paired computer, within its ceiling", OwnProject: true},
	{Name: "cross-project-message", Default: Allow, Detail: "Send a queued message to a session in another project on this computer"},
	{Name: "cross-project-steer", Default: Notify, Detail: "Steer, interrupt or send now to a session in another project on this computer, or to its coordinator, within its ceiling"},
	{Name: "session-create-full", Default: Notify, Detail: "From a session the person started outside Agenthail, start one in bypassPermissions or never-approval mode"},
	forPerson("project-any", "Add, change, archive or remove any project, on any computer"),
	forPerson("coordinator-any", "Set, start or clear the lead of any project"),
	forPerson("relay-any", "Relay replies between any two sessions, or remove any relay"),
	forPerson("channel-any", "Create, change or delete any channel"),
	forPerson("queue-any", "Cancel or clear queued messages to any session"),
	forPerson("history-read", "Read the delivery history and queue of every session"),
	forPerson("brief", "Ask a project's lead to catch the person up"),
	forPerson("notion-create", "Start a Notion thread in the person's workspace"),
	forPerson("answer-for-person", "Answer requests as the person, including always-allow rules"),
	forPerson("mode-raise", "Set a session's permission mode above its ceiling"),
	forPerson("host-setting", "Turn this computer's resuming after a usage limit on or off"),
}

// BuiltIn gives each agent what it could do before roles: any session is a
// worker, a project's coordinator a lead, the voice operator the operator,
// and an identity reaching this computer from elsewhere the remote role.
var BuiltIn = map[string]Role{
	Worker:   {Description: "Any session without another role: it acts on the sessions it started.", Reach: ReachDescendants},
	Lead:     {Description: "A project's coordinator: it manages its own project and its sessions.", Reach: ReachProject},
	Operator: {Description: "The person's voice operator: it acts for the person everywhere.", Reach: ReachAll},
	Remote:   {Description: "An agent identity that reaches this computer from elsewhere: it acts for the person everywhere.", Reach: ReachAll},
}

// BaseCapabilities are what every role holds.
var BaseCapabilities = []string{"read.self", "read.project", "spawn", "control.descendants", "requests.answer", "ledger.read", "ledger.write", "browser.drive", "simulator.drive"}

var reachCapabilities = map[string][]string{
	ReachDescendants: nil,
	ReachProject:     {"control.project"},
	ReachAll:         {"control.all", "read.all"},
}

func ActionNamed(name string) (Action, bool) {
	for _, action := range Actions {
		if action.Name == name {
			return action, true
		}
	}
	return Action{}, false
}

func ValidDecision(value string) bool {
	return value == Allow || value == Notify || value == Ask
}

func ActionNames() []string {
	names := make([]string, 0, len(Actions))
	for _, action := range Actions {
		names = append(names, action.Name)
	}
	sort.Strings(names)
	return names
}

func (r Role) DefaultDecision(action Action) string {
	switch {
	case r.Reach == ReachAll && action.AllReach != "":
		return action.AllReach
	case action.OwnProject && r.Reach == ReachDescendants:
		return Ask
	}
	return action.Default
}

// Decision is the role's decision for an action; one it does not know is the
// person's.
func (r Role) Decision(name string) string {
	action, known := ActionNamed(name)
	if !known {
		return Ask
	}
	if value, set := r.Actions[name]; set && ValidDecision(value) {
		return value
	}
	return r.DefaultDecision(action)
}

func (r Role) DepthLimit() int {
	if r.Depth <= 0 {
		return DefaultDepth
	}
	return r.Depth
}

func (r Role) RunningLimit() int {
	if r.Running <= 0 {
		return DefaultRunning
	}
	return r.Running
}

func (r Role) Sees(tool string) bool {
	return r.Tools == nil || slices.Contains(r.Tools, tool)
}

func (r Role) AllLedgers() bool {
	if r.Ledgers == nil {
		return r.Reach == ReachAll
	}
	return slices.Contains(r.Ledgers, LedgersAll)
}

// ReachesLedger is whether the role reads and writes the ledger of the
// project with this id when it is not the caller's own.
func (r Role) ReachesLedger(projectID string) bool {
	return r.AllLedgers() || slices.Contains(r.Ledgers, projectID)
}

func (r Role) Capabilities() []string {
	return append(slices.Clone(BaseCapabilities), reachCapabilities[r.Reach]...)
}

// Validate reports the first thing wrong with a role the person wrote.
func (r Role) Validate() error {
	if _, ok := reachCapabilities[r.Reach]; !ok {
		return fmt.Errorf("a role's reach is descendants, project or all, not %q", r.Reach)
	}
	for name, decision := range r.Actions {
		if _, known := ActionNamed(name); !known {
			return fmt.Errorf("no agent action is named %q; the actions are %s", name, strings.Join(ActionNames(), ", "))
		}
		if !ValidDecision(decision) {
			return fmt.Errorf("a decision is allow, notify or ask, not %q", decision)
		}
	}
	for _, tool := range r.Tools {
		if _, known := agenttools.Lookup(tool); !known {
			return fmt.Errorf("no agenthail tool is named %q", tool)
		}
	}
	for _, project := range r.Ledgers {
		if strings.TrimSpace(project) == "" {
			return fmt.Errorf("a role's ledgers are project ids or %q, not an empty name", LedgersAll)
		}
	}
	if r.Depth < 0 || r.Running < 0 {
		return fmt.Errorf("a limit is 0 (the default) or more")
	}
	return nil
}

// Normalized drops decisions equal to the role's defaults and a tool list
// naming every tool, so a stored role holds only what the person changed.
func (r Role) Normalized() Role {
	actions := map[string]string{}
	for name, decision := range r.Actions {
		action, _ := ActionNamed(name)
		if decision != r.DefaultDecision(action) {
			actions[name] = decision
		}
	}
	r.Actions = nil
	if len(actions) > 0 {
		r.Actions = actions
	}
	if r.Tools != nil {
		tools := slices.Clone(r.Tools)
		sort.Strings(tools)
		r.Tools = slices.Compact(tools)
		if len(r.Tools) == len(agenttools.Tools) {
			r.Tools = nil
		}
	}
	if r.Ledgers != nil {
		ledgers := slices.Clone(r.Ledgers)
		sort.Strings(ledgers)
		r.Ledgers = slices.Compact(ledgers)
		if slices.Contains(r.Ledgers, LedgersAll) {
			r.Ledgers = []string{LedgersAll}
		}
	}
	return r
}
