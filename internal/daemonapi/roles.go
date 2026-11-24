package daemonapi

import "github.com/zm2231/agenthail/internal/agentroles"

type AgentRoleView struct {
	Name        string            `json:"name"`
	BuiltIn     bool              `json:"builtIn"`
	Edited      bool              `json:"edited"`
	Description string            `json:"description"`
	Reach       string            `json:"reach"`
	Decisions   map[string]string `json:"decisions"`
	// EffectiveDefaults is each action's decision when the role sets none,
	// which depends on its reach.
	EffectiveDefaults map[string]string `json:"effectiveDefaults"`
	// Definition is the role as stored, which a change edits and puts back.
	Definition agentroles.Role `json:"definition"`
	Tools      []string        `json:"tools"`
	AllTools   bool            `json:"allTools"`
	// Ledgers are the project ids whose ledgers the role reaches besides
	// its own; AllLedgers is every project's.
	Ledgers    []string `json:"ledgers"`
	AllLedgers bool     `json:"allLedgers"`
	Depth      int      `json:"depth"`
	Running    int      `json:"running"`
}

// AgentRoleExplanation is the role a session or agent identity acts under
// and why: Source names the assignment or what the caller is, and Replaces
// the role it would hold by what it is when an assignment gave it another.
type AgentRoleExplanation struct {
	Target   string        `json:"target"`
	Role     AgentRoleView `json:"role"`
	Source   string        `json:"source"`
	Replaces string        `json:"replaces,omitempty"`
}

type AgentActionView struct {
	Name       string `json:"name"`
	Default    string `json:"default"`
	Detail     string `json:"detail"`
	OwnProject bool   `json:"ownProject"`
	AllReach   string `json:"allReach,omitempty"`
}

type AgentRolesView struct {
	Roles       []AgentRoleView           `json:"roles"`
	Assignments []AgentRoleAssignmentView `json:"assignments"`
	Actions     []AgentActionView         `json:"actions"`
	Tools       []string                  `json:"tools"`
	Identities  []AgentIdentityRoleView   `json:"identities"`
}

// AgentIdentityRoleView is the role an agent identity acts under: the remote
// role's when it has remote access, else the one its local sends get.
type AgentIdentityRoleView struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type AgentRoleAssignmentView struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Role   string `json:"role"`
	// Stale says why the assignment gives no session its role, if it does not.
	Stale string `json:"stale,omitempty"`
}

type AgentRoleRequest struct {
	// Op is put, which writes the whole role; delete, which removes a role
	// the person made or returns a built-in one to how Agenthail ships it;
	// assign, which gives the role Name to what Kind and Target name; or
	// unassign, which takes that assignment away.
	Op   string          `json:"op"`
	Name string          `json:"name"`
	Role agentroles.Role `json:"role"`
	// Kind is session, identity or alias. A session's Target may be its id
	// or its alias, and is stored as its conversation id.
	Kind   string `json:"kind,omitempty"`
	Target string `json:"target,omitempty"`
}
