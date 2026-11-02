package ledger

import (
	"encoding/json"
	"fmt"
	"time"
)

type AttachAs string

const (
	AsNewAsk       AttachAs = "new-ask"
	AsExtends      AttachAs = "extends"
	AsConstrains   AttachAs = "constrains"
	AsRefines      AttachAs = "refines"
	AsReacts       AttachAs = "reacts"
	AsDelegates    AttachAs = "delegates"
	AsDecides      AttachAs = "decides"
	AsConversation AttachAs = "conversation"
)

const ProjectTarget = "project"

type AttachRequest struct {
	ProjectKey  string       `json:"projectKey"`
	SessionID   string       `json:"sessionId"`
	UtteranceID string       `json:"utteranceId,omitempty"`
	As          AttachAs     `json:"as"`
	To          string       `json:"to,omitempty"`
	Clauses     []string     `json:"clauses,omitempty"`
	AskType     AskType      `json:"askType,omitempty"`
	Source      ClauseSource `json:"source,omitempty"`
	Adopt       bool         `json:"adopt,omitempty"`
	Reject      bool         `json:"reject,omitempty"`
	Delegate    string       `json:"delegate,omitempty"`
}

type AttachResult struct {
	ProjectKey string   `json:"projectKey"`
	GoalID     string   `json:"goalId,omitempty"`
	Effects    []string `json:"effects"`
	Readback   string   `json:"readback"`
}

type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return r.Reason }

func refuse(format string, args ...any) error {
	return &Refusal{Reason: fmt.Sprintf(format, args...)}
}

func (s *Store) Attach(req AttachRequest) (AttachResult, error) {
	if req.ProjectKey == "" || req.SessionID == "" || req.As == "" {
		return AttachResult{}, refuse("project, acting session and --as are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return AttachResult{}, err
	}
	defer tx.Rollback()
	board, err := loadBoard(tx, req.ProjectKey)
	if err != nil {
		return AttachResult{}, err
	}
	plan := &attachPlan{req: req, board: board, ids: &idIssuer{q: tx, projectKey: req.ProjectKey}}
	if err := plan.build(); err != nil {
		return AttachResult{}, err
	}
	if req.UtteranceID != "" {
		other, err := attachedElsewhere(tx, req.ProjectKey, req.UtteranceID)
		if err != nil {
			return AttachResult{}, err
		}
		if other != nil {
			return AttachResult{}, refuse("%s is attached elsewhere: %s", req.UtteranceID, other)
		}
	}
	attach, err := json.Marshal(AttachPayload{UtteranceID: req.UtteranceID, SessionID: req.SessionID, As: req.As, To: req.To, GoalID: plan.goalID, Effects: plan.effects})
	if err != nil {
		return AttachResult{}, err
	}
	now := time.Now().UTC()
	for _, e := range append(plan.events, Event{Type: EventAttach, Payload: attach}) {
		e.ProjectKey, e.ActorSessionID, e.CreatedAt = req.ProjectKey, req.SessionID, now
		inserted, err := insertEvent(tx, `INSERT`, e)
		if err != nil {
			return AttachResult{}, err
		}
		if err := board.Apply(inserted); err != nil {
			return AttachResult{}, err
		}
	}
	readback, err := plan.readback(tx, board)
	if err != nil {
		return AttachResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AttachResult{}, err
	}
	return AttachResult{ProjectKey: req.ProjectKey, GoalID: plan.goalID, Effects: plan.effects, Readback: readback}, nil
}

type attachPlan struct {
	req       AttachRequest
	board     *Board
	ids       *idIssuer
	utterance *UtteranceView
	goalID    string
	events    []Event
	effects   []string
	summary   string
}

func personCapture(capturedBy string) bool {
	return capturedBy == CapturedByHook || capturedBy == CapturedByDelivery || capturedBy == CapturedByJournal
}

func (p *attachPlan) build() error {
	req := p.req
	if req.UtteranceID != "" {
		u := p.board.utterances[req.UtteranceID]
		if u == nil || !personCapture(u.CapturedBy) {
			return refuse("%s is not a captured person utterance in %s", req.UtteranceID, req.ProjectKey)
		}
		p.utterance = u
	}
	switch req.As {
	case AsNewAsk, AsExtends, AsConstrains:
		return p.addClauses()
	case AsRefines, AsReacts, AsDelegates, AsDecides, AsConversation:
	default:
		return refuse("unknown --as %q; use new-ask, extends, constrains, refines, reacts, delegates, decides or conversation", req.As)
	}
	if req.Source != "" {
		return refuse("--source applies only to new clauses (new-ask, extends, constrains)")
	}
	if p.utterance == nil {
		return refuse("--as %s needs the person's utterance it attaches", req.As)
	}
	switch req.As {
	case AsRefines:
		return p.refine()
	case AsReacts:
		return p.react()
	case AsDelegates:
		return p.delegate()
	case AsDecides:
		return p.decide()
	}
	if req.To != "" || len(req.Clauses) > 0 {
		return refuse("--as conversation takes no --to or --clause")
	}
	p.summary = req.UtteranceID + " is conversation"
	return nil
}

func (p *attachPlan) add(typ EventType, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	p.events = append(p.events, Event{Type: typ, Payload: data})
	return nil
}

func (p *attachPlan) label(id string) string {
	if g := p.board.goals[id]; g != nil {
		return fmt.Sprintf("%s (%s)", g.ID, g.Name)
	}
	if id == "" || id == ProjectTarget {
		return "the project"
	}
	return id
}

func (p *attachPlan) speaker() string {
	if p.utterance == nil {
		return p.req.SessionID
	}
	return p.utterance.SpeakerID
}

func (p *attachPlan) utteranceID() string {
	if p.utterance == nil {
		return ""
	}
	return p.utterance.ID
}
