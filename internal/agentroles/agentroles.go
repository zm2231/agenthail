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

// Role holds only the decisions that differ from each action's default for
// the role's reach. Tools null means every agenthail tool, and an empty list
// none; a limit of 0 is the default.
type Role struct {
	Description string            `json:"description,omitempty"`
	Reach       string            `json:"reach"`
	Actions     map[string]string `json:"actions,omitempty"`
	Tools       []string          `json:"tools"`
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
}

// A new action is one more row here. Actions outside this list, such as
// approving extensions or answering for the person, stay the person's.
var Actions = []Action{
	{"session-create-remote", Notify, "Start a session on a paired computer", false},
	{"extension-build", Notify, "Start an extension builder", false},
	{"project-add", Allow, "Add a folder on this computer that is in no project", true},
	{"project-rename", Allow, "Rename its own project", true},
	{"project-link", Allow, "Link a project into its own", true},
	{"project-unlink", Allow, "Unlink a project from its own", true},
	{"project-setting", Allow, "Turn its own project's tools on or off", true},
	{"project-archive", Notify, "Archive its own project", true},
	{"project-unarchive", Allow, "Unarchive its own project", true},
	{"project-remove", Notify, "Remove its own project from Agenthail; the files stay", true},
	{"coordinator-set", Allow, "Make a session in its own project the lead", true},
	{"coordinator-new", Allow, "Start a new lead for its own project", true},
	{"coordinator-clear", Notify, "Clear its own project's lead", true},
	{"relay-add", Allow, "Relay replies between sessions in its own project", true},
	{"relay-remove", Allow, "Remove such a relay", true},
	{"queue-cancel", Allow, "Cancel a queued message in its own project", true},
	{"queue-clear", Allow, "Clear queued messages in its own project", true},
	{"channel-create", Allow, "Create a channel", true},
	{"channel-rename", Allow, "Rename a channel whose sessions are all in its project", true},
	{"channel-add", Allow, "Add a session in its project to a channel whose sessions are all in its project", true},
	{"channel-remove", Allow, "Remove a session from a channel whose sessions are all in its project", true},
	{"channel-delete", Notify, "Delete a channel whose sessions are all in its project", true},
	{"limit-resume", Notify, "Turn resuming its own project's sessions a usage limit stopped on or off", true},
	{"cross-project-message", Allow, "Send a queued message to a session in another project on this computer", false},
	{"cross-project-steer", Notify, "Steer, interrupt or send now to a session in another project on this computer, or to its coordinator, within its ceiling", false},
	{"session-create-full", Notify, "From a session the person started outside Agenthail, start one in bypassPermissions or never-approval mode", false},
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
	if action.OwnProject && r.Reach == ReachDescendants {
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
	return r
}
