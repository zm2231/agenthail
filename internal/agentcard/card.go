package agentcard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Root string `json:"root"`
}

type Lead struct {
	Ref     string `json:"ref"`
	Session string `json:"session,omitempty"`
	Self    bool   `json:"self,omitempty"`
}

type Origin struct {
	Via     registry.OriginVia `json:"via"`
	Session string             `json:"session,omitempty"`
	Ref     string             `json:"ref,omitempty"`
	Name    string             `json:"name,omitempty"`
}

type Ask struct {
	Tool       string `json:"tool,omitempty"`
	Answerable bool   `json:"answerable"`
	Phone      bool   `json:"phone"`
}

type Command struct {
	Use  string `json:"use"`
	Line string `json:"line"`
}

type Card struct {
	Resolved    bool                `json:"resolved"`
	Reason      string              `json:"reason,omitempty"`
	Session     string              `json:"session,omitempty"`
	Surface     surface.SurfaceKind `json:"surface,omitempty"`
	Runtime     surface.RuntimeKind `json:"runtime,omitempty"`
	Alias       string              `json:"alias,omitempty"`
	Host        string              `json:"host,omitempty"`
	Cwd         string              `json:"cwd,omitempty"`
	Project     *Project            `json:"project,omitempty"`
	Lead        *Lead               `json:"lead,omitempty"`
	LeadProblem string              `json:"leadProblem,omitempty"`
	StartedBy   *Origin             `json:"startedBy,omitempty"`
	ReportTo    string              `json:"reportTo,omitempty"`
	Ask         *Ask                `json:"ask,omitempty"`
	Commands    []Command           `json:"commands,omitempty"`
}

func Unresolved(reason string) Card { return Card{Reason: reason} }

// Project returns nil for a folder outside every project. PromptsHeld
// reports whether Agenthail holds a terminal Claude session's prompts.
type Sources struct {
	Registry    *registry.Registry
	Project     func(ctx context.Context, cwd string) (*Project, error)
	LeadRef     func(registry.CoordinatorSpec) string
	PromptsHeld func(surface.Session) bool
}

func (s Sources) For(ctx context.Context, session surface.Session) (Card, error) {
	card := Card{Resolved: true, Session: session.ID, Surface: session.Surface, Runtime: session.RuntimeKind, Cwd: session.Cwd}
	alias, err := s.Registry.ReverseAlias(session.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Card{}, fmt.Errorf("read alias: %w", err)
	}
	card.Alias = alias
	if card.Host, err = s.hostLabel(); err != nil {
		return Card{}, err
	}
	if session.Cwd != "" {
		if card.Project, err = s.Project(ctx, session.Cwd); err != nil {
			return Card{}, err
		}
	}
	if card.Project != nil {
		if err := s.lead(&card); err != nil {
			return Card{}, err
		}
	}
	if card.StartedBy, err = s.origin(session.ID); err != nil {
		return Card{}, err
	}
	if card.Ask, err = s.ask(session); err != nil {
		return Card{}, err
	}
	card.ReportTo = reportTo(card)
	card.Commands = commands(card)
	return card, nil
}

func (s Sources) hostLabel() (string, error) {
	hosts, err := s.Registry.ListHosts()
	if err != nil {
		return "", fmt.Errorf("list paired hosts: %w", err)
	}
	if len(hosts) == 0 {
		return "", nil
	}
	identity, err := s.Registry.HostIdentity()
	if err != nil {
		return "", fmt.Errorf("read host identity: %w", err)
	}
	return identity.Name, nil
}

func (s Sources) lead(card *Card) error {
	specs, err := s.Registry.ProjectCoordinators(card.Project.ID)
	if err != nil {
		return fmt.Errorf("read the project's coordinator: %w", err)
	}
	switch len(specs) {
	case 0:
		return nil
	case 1:
	default:
		refs := make([]string, 0, len(specs))
		for _, spec := range specs {
			refs = append(refs, s.LeadRef(spec))
		}
		card.LeadProblem = fmt.Sprintf("this project has coordinators %s; @lead refuses until one is chosen", strings.Join(refs, " and "))
		return nil
	}
	spec := specs[0]
	lead := &Lead{Ref: s.LeadRef(spec), Session: spec.SessionID}
	if spec.HostID == "" {
		id, err := s.Registry.CoordinatorSessionID(spec)
		if err != nil {
			return fmt.Errorf("read the coordinator's conversation: %w", err)
		}
		lead.Session = id
		lead.Self = id != "" && id == card.Session
	}
	card.Lead = lead
	return nil
}

func (s Sources) origin(sessionID string) (*Origin, error) {
	stored, found, err := s.Registry.SessionOrigin(sessionID)
	if err != nil {
		return nil, fmt.Errorf("read who started the session: %w", err)
	}
	if !found {
		return nil, nil
	}
	origin := &Origin{Via: stored.Via, Session: stored.CreatorID}
	if stored.CreatorID == "" {
		return origin, nil
	}
	creator, err := s.Registry.Session(stored.CreatorID)
	if err == nil && creator.Name != "" {
		origin.Name = creator.Name
	}
	if surface.IsUnrepliableSender(stored.CreatorID) {
		return origin, nil
	}
	if alias, err := s.Registry.ReverseAlias(stored.CreatorID); err == nil && alias != "" {
		origin.Ref = "@" + alias
	} else {
		origin.Ref = stored.CreatorID
	}
	return origin, nil
}

func (s Sources) ask(session surface.Session) (*Ask, error) {
	ask := &Ask{}
	switch session.RuntimeKind {
	case surface.RuntimeClaudeHost:
		ask.Tool, ask.Answerable = "AskUserQuestion", true
	case surface.RuntimeClaudeCLI:
		ask.Tool, ask.Answerable = "AskUserQuestion", s.PromptsHeld != nil && s.PromptsHeld(session)
	case surface.RuntimeClaudeBackground:
		ask.Tool = "AskUserQuestion"
	case surface.RuntimeCodexAppServer, surface.RuntimeCodexDesktop:
		ask.Tool = "request_user_input"
	default:
		return nil, nil
	}
	targets, err := s.Registry.DevicePushTargets()
	if err != nil {
		return nil, fmt.Errorf("read paired phones: %w", err)
	}
	ask.Phone = len(targets) > 0
	return ask, nil
}

// The coordinator sees an agent-started session's reports in history, so
// they go to the agent that started it.
func reportTo(card Card) string {
	if card.StartedBy != nil && card.StartedBy.Ref != "" {
		return card.StartedBy.Ref
	}
	if card.Lead != nil && !card.Lead.Self {
		return "@lead"
	}
	return ""
}

func commands(card Card) []Command {
	if card.Surface == surface.KindNotion {
		return nil
	}
	var out []Command
	if card.ReportTo != "" {
		out = append(out, Command{Use: "report a result or a blocker", Line: fmt.Sprintf(`agenthail send %s "<result>"`, card.ReportTo)})
	}
	out = append(out,
		Command{Use: "message another session", Line: `agenthail send <@alias|session-id> "<message>"`},
		Command{Use: "find sessions and their aliases", Line: "agenthail list --json"},
		Command{Use: "read a session's latest output", Line: "agenthail last <@alias|session-id>"},
	)
	if card.Project != nil {
		out = append(out, Command{Use: "tell every session in this project", Line: fmt.Sprintf(`agenthail channel send '#%s' "<message>"`, card.Project.ID)})
	}
	return append(out,
		Command{Use: "start a helper session", Line: `agenthail thread create <claude|codex> "<task>" --alias <name>`},
		Command{Use: "check who you are now", Line: "agenthail whoami --json"},
	)
}
