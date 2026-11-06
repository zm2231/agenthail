package ledger

import (
	"encoding/json"
	"fmt"
	"strings"
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
	AsAnswers      AttachAs = "answers"
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
	Ack         bool         `json:"ack,omitempty"`
	Delegate    string       `json:"delegate,omitempty"`
	Covers      []string     `json:"covers,omitempty"`
}

type AttachResult struct {
	ProjectKey string   `json:"projectKey"`
	GoalID     string   `json:"goalId,omitempty"`
	Effects    []string `json:"effects"`
	Readback   string   `json:"readback"`
}

type Refusal struct {
	Reason  string
	MovedTo string
}

func (r *Refusal) Error() string { return r.Reason }

func refuse(format string, args ...any) error {
	return &Refusal{Reason: fmt.Sprintf(format, args...)}
}

func (s *Store) Attach(req AttachRequest) (AttachResult, error) {
	if req.ProjectKey == "" || req.SessionID == "" {
		return AttachResult{}, refuse("project and acting session are required")
	}
	board, release, err := s.lockBoard(req.ProjectKey)
	if err != nil {
		return AttachResult{}, err
	}
	defer release()
	tx, err := s.begin(req.ProjectKey)
	if err != nil {
		return AttachResult{}, err
	}
	defer tx.Rollback()
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
	attach, err := json.Marshal(AttachPayload{UtteranceID: req.UtteranceID, SessionID: req.SessionID, As: plan.req.As, To: plan.req.To, GoalID: plan.goalID, Effects: plan.effects})
	if err != nil {
		return AttachResult{}, err
	}
	now := time.Now().UTC()
	for _, e := range append(plan.events, Event{Type: EventAttach, Payload: attach}) {
		e.ProjectKey, e.ActorSessionID, e.CreatedAt = req.ProjectKey, req.SessionID, now
		if _, err := insertEvent(tx, `INSERT`, e); err != nil {
			return AttachResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AttachResult{}, err
	}
	if err := s.catchUp(board, req.ProjectKey); err != nil {
		return AttachResult{}, err
	}
	readback, err := plan.readback(s.read, board)
	if err != nil {
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
	notes     []string

	madeGoal, madeGoalName string
}

func personCapture(capturedBy string) bool {
	return capturedBy == CapturedByHook || capturedBy == CapturedByDelivery || capturedBy == CapturedByJournal
}

func (p *attachPlan) build() error {
	if p.req.UtteranceID != "" {
		u := p.board.utterances[p.req.UtteranceID]
		if u == nil || !personCapture(u.CapturedBy) {
			return refuse("%s is not a captured person utterance in %s", p.req.UtteranceID, p.req.ProjectKey)
		}
		p.utterance = u
	}
	p.inferAs()
	if err := p.resolveTarget(); err != nil {
		return err
	}
	if err := p.relate(); err != nil {
		return err
	}
	if len(p.notes) > 0 {
		p.summary += " (" + strings.Join(p.notes, "; ") + ")"
	}
	return nil
}

func (p *attachPlan) relate() error {
	req := p.req
	switch req.As {
	case AsNewAsk, AsExtends, AsConstrains:
		return p.addClauses()
	case AsRefines, AsReacts, AsDelegates, AsDecides, AsConversation, AsAnswers:
	default:
		return refuse("unknown --as %q; use new-ask, extends, constrains, refines, reacts, delegates, decides, answers or conversation", req.As)
	}
	if req.Source != "" || len(req.Covers) > 0 {
		return refuse("--source and --covers apply only to new clauses (new-ask, extends, constrains)")
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
	case AsAnswers:
		return p.answer()
	}
	p.summary = req.UtteranceID + " is conversation"
	if dropped := p.unrecorded(); dropped != "" {
		p.summary += "; " + dropped + " not recorded: conversation binds nothing (attach --as constrains to bind it)"
	}
	p.req.To = ""
	return nil
}

func (p *attachPlan) unrecorded() string {
	var parts []string
	if p.req.To != "" {
		parts = append(parts, "--to "+p.req.To)
	}
	if n := len(p.req.Clauses); n == 1 {
		parts = append(parts, "the --clause")
	} else if n > 1 {
		parts = append(parts, fmt.Sprintf("%d --clause texts", n))
	}
	return strings.Join(parts, " and ")
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
	if id != "" && id == p.madeGoal {
		return fmt.Sprintf("%s (%s)", id, p.madeGoalName)
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
