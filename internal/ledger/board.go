package ledger

import (
	"encoding/json"
	"sync"
)

type Goal struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	ParentGoalID string    `json:"parentGoalId,omitempty"`
	Asks         []string  `json:"asks,omitempty"`
	Constraints  []*Clause `json:"constraints,omitempty"`
}

type Ask struct {
	ID              string    `json:"id"`
	GoalID          string    `json:"goalId,omitempty"`
	Type            AskType   `json:"type"`
	Principal       string    `json:"principal"`
	SourceUtterance string    `json:"sourceUtteranceId"`
	Delegates       []string  `json:"delegates,omitempty"`
	Clauses         []*Clause `json:"clauses"`
}

type Clause struct {
	ID             string       `json:"id"`
	AskID          string       `json:"askId,omitempty"`
	GoalID         string       `json:"goalId,omitempty"`
	Kind           string       `json:"kind,omitempty"`
	Text           string       `json:"text"`
	State          ClauseState  `json:"state"`
	Source         ClauseSource `json:"source,omitempty"`
	OwnerSessionID string       `json:"ownerSessionId,omitempty"`
	ProposedBy     string       `json:"proposedBy,omitempty"`
	Sources        []string     `json:"sourceUtteranceIds,omitempty"`
	SelfReported   bool         `json:"selfReported,omitempty"`
	ProgressRef    string       `json:"progressRef,omitempty"`
	Cuts           []Cut        `json:"cuts,omitempty"`
	Waits          []Wait       `json:"waits,omitempty"`
}

type UtteranceView struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	SpeakerID  string   `json:"speakerId"`
	TargetID   string   `json:"targetId"`
	CapturedBy string   `json:"capturedBy"`
	At         string   `json:"ts"`
	GoalIDs    []string `json:"goalIds,omitempty"`
	Attached   bool     `json:"attached"`
}

type Cut struct {
	ID         string `json:"id"`
	Reason     string `json:"reason"`
	Acked      bool   `json:"acked"`
	Withdrawn  bool   `json:"withdrawn,omitempty"`
	QuestionID string `json:"questionId,omitempty"`
}

type Wait struct {
	Condition  string `json:"condition"`
	DeadlineMs int64  `json:"deadlineMs"`
	Resolved   string `json:"resolved,omitempty"`
}

type Decision struct {
	ID              string `json:"id"`
	GoalID          string `json:"goalId,omitempty"`
	Version         int    `json:"version"`
	Text            string `json:"text"`
	Principal       string `json:"principal"`
	SourceUtterance string `json:"sourceUtteranceId,omitempty"`
}

type Board struct {
	mu                 sync.RWMutex
	goals              map[string]*Goal
	asks               map[string]*Ask
	clauses            map[string]*Clause
	decisions          map[string]*Decision
	utterances         map[string]*UtteranceView
	utteranceOrder     []string
	projectConstraints []*Clause
	targetCuts         []*TargetCut
	questions          map[string]*Question
	applied            int64
}

func NewBoard() *Board {
	b := &Board{}
	b.reset()
	return b
}

func (b *Board) reset() {
	b.goals = map[string]*Goal{}
	b.asks = map[string]*Ask{}
	b.clauses = map[string]*Clause{}
	b.decisions = map[string]*Decision{}
	b.utterances = map[string]*UtteranceView{}
	b.utteranceOrder = nil
	b.projectConstraints = nil
	b.targetCuts = nil
	b.questions = map[string]*Question{}
	b.applied = 0
}

func (b *Board) Apply(event Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var err error
	switch event.Type {
	case EventGoal:
		err = b.applyGoal(event.Payload)
	case EventAsk:
		err = b.applyAsk(event.Payload)
	case EventClause:
		err = b.applyClause(event.Payload)
	case EventClauseState:
		err = b.applyClauseState(event.Payload)
	case EventDecision:
		err = b.applyDecision(event.Payload)
	case EventDecisionVer:
		err = b.applyDecisionVersion(event.Payload)
	case EventCut:
		err = b.applyCut(event.Payload)
	case EventCutAck:
		err = b.applyCutAck(event.Payload)
	case EventWait:
		err = b.applyWait(event.Payload)
	case EventWaitResolved:
		err = b.applyWaitResolved(event.Payload)
	case EventDelegation:
		err = b.applyDelegation(event.Payload)
	case EventSupersession:
		err = b.applySupersession(event.Payload)
	case EventUtterance:
		err = b.applyUtterance(event.Payload)
	case EventAttach:
		err = b.applyAttach(event.Payload)
	case EventClauseSource:
		err = b.applyClauseSource(event.Payload)
	case EventQuestion:
		err = b.applyQuestion(event.Payload)
	case EventQuestionRes:
		err = b.applyQuestionResolved(event.Payload)
	case EventQuestionEsc:
		err = b.applyQuestionEscalated(event.Payload)
	}
	if err == nil {
		b.applied = event.Seq
	}
	return err
}

func (b *Board) AppliedSeq() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.applied
}

func (b *Board) OpenAsks() []Ask {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var result []Ask
	for _, ask := range b.asks {
		hasOpen := false
		for _, c := range ask.Clauses {
			if c.State != StateReported && c.State != StateRejected && !clauseFullyCut(c) {
				hasOpen = true
				break
			}
		}
		if hasOpen {
			result = append(result, *ask)
		}
	}
	return result
}

func (b *Board) AllAsks() []Ask {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Ask, 0, len(b.asks))
	for _, ask := range b.asks {
		result = append(result, *ask)
	}
	return result
}

func (b *Board) Goals() []Goal {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Goal, 0, len(b.goals))
	for _, g := range b.goals {
		result = append(result, *g)
	}
	return result
}

func (b *Board) Decisions() []Decision {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Decision, 0, len(b.decisions))
	for _, d := range b.decisions {
		result = append(result, *d)
	}
	return result
}

func (b *Board) ProjectConstraints() []Clause {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]Clause, 0, len(b.projectConstraints))
	for _, c := range b.projectConstraints {
		result = append(result, *c)
	}
	return result
}

func (b *Board) Utterances() []UtteranceView {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]UtteranceView, 0, len(b.utteranceOrder))
	for _, ref := range b.utteranceOrder {
		result = append(result, *b.utterances[ref])
	}
	return result
}

func (b *Board) ClausesOwnedBy(sessionID string) []Clause {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var result []Clause
	for _, c := range b.clauses {
		if c.OwnerSessionID == sessionID && c.State != StateReported && c.State != StateRejected && !clauseFullyCut(c) {
			result = append(result, *c)
		}
	}
	return result
}

func clauseFullyCut(c *Clause) bool {
	for _, cut := range c.Cuts {
		if cut.Acked {
			return true
		}
	}
	return false
}

func (b *Board) Rebuild(events []Event) error {
	b.mu.Lock()
	b.reset()
	b.mu.Unlock()
	for _, e := range events {
		if err := b.Apply(e); err != nil {
			return err
		}
	}
	return nil
}

func (b *Board) applyGoal(data []byte) error {
	var p GoalPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	b.goals[p.GoalID] = &Goal{
		ID:           p.GoalID,
		Name:         p.Name,
		ParentGoalID: p.ParentGoalID,
	}
	return nil
}

func (b *Board) applyAsk(data []byte) error {
	var p AskPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	ask := &Ask{
		ID:              p.AskID,
		GoalID:          p.GoalID,
		Type:            p.Type,
		Principal:       p.Principal,
		SourceUtterance: p.SourceUtterance,
	}
	b.asks[p.AskID] = ask
	if g, ok := b.goals[p.GoalID]; ok {
		g.Asks = append(g.Asks, p.AskID)
	}
	return nil
}

func (b *Board) applyClause(data []byte) error {
	var p ClausePayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c := &Clause{
		ID:             p.ClauseID,
		AskID:          p.AskID,
		GoalID:         p.GoalID,
		Kind:           p.Kind,
		Text:           p.Text,
		State:          p.State,
		Source:         p.Source,
		OwnerSessionID: p.OwnerSessionID,
		ProposedBy:     p.ProposedBy,
	}
	if p.SourceUtteranceID != "" {
		c.Sources = []string{p.SourceUtteranceID}
	}
	b.clauses[p.ClauseID] = c
	switch ask, ok := b.asks[p.AskID]; {
	case ok:
		ask.Clauses = append(ask.Clauses, c)
	case p.Kind != ClauseKindConstraint:
	case p.GoalID != "" && b.goals[p.GoalID] != nil:
		b.goals[p.GoalID].Constraints = append(b.goals[p.GoalID].Constraints, c)
	case p.GoalID == "":
		b.projectConstraints = append(b.projectConstraints, c)
	}
	return nil
}

func (b *Board) applyClauseState(data []byte) error {
	var p ClauseStatePayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c, ok := b.clauses[p.ClauseID]
	if !ok {
		return nil
	}
	c.State = p.ToState
	if p.ProgressRef != "" {
		c.ProgressRef = p.ProgressRef
		c.SelfReported = true
	}
	return nil
}

func (b *Board) applyDecision(data []byte) error {
	var p DecisionPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	b.decisions[p.DecisionID] = &Decision{
		ID:              p.DecisionID,
		GoalID:          p.GoalID,
		Version:         p.Version,
		Text:            p.Text,
		Principal:       p.Principal,
		SourceUtterance: p.SourceUtterance,
	}
	return nil
}

func (b *Board) applyDecisionVersion(data []byte) error {
	var p DecisionPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if d, ok := b.decisions[p.DecisionID]; ok {
		d.Version = p.Version
		d.Text = p.Text
		d.Principal = p.Principal
	}
	return nil
}

func (b *Board) applyCut(data []byte) error {
	var p CutPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	cut := Cut{ID: p.CutID, Reason: p.Reason, Acked: p.Acked, QuestionID: p.QuestionID}
	if c, ok := b.clauses[p.ClauseID]; ok {
		c.Cuts = append(c.Cuts, cut)
		return nil
	}
	if p.Target != "" {
		b.targetCuts = append(b.targetCuts, &TargetCut{Cut: cut, Target: p.Target})
	}
	return nil
}

func (b *Board) applyCutAck(data []byte) error {
	var p CutAckPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	for _, c := range b.clauses {
		for i := range c.Cuts {
			if c.Cuts[i].ID == p.CutID {
				c.Cuts[i].Acked = true
				return nil
			}
		}
	}
	for _, t := range b.targetCuts {
		if t.ID == p.CutID {
			t.Acked = true
		}
	}
	return nil
}

func (b *Board) applyWait(data []byte) error {
	var p WaitPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c, ok := b.clauses[p.ClauseID]
	if !ok {
		return nil
	}
	c.Waits = append(c.Waits, Wait{Condition: p.Condition, DeadlineMs: p.DeadlineMs})
	return nil
}

func (b *Board) applyWaitResolved(data []byte) error {
	var p WaitResolvedPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c, ok := b.clauses[p.ClauseID]
	if !ok {
		return nil
	}
	for i := range c.Waits {
		if c.Waits[i].Resolved == "" {
			c.Waits[i].Resolved = p.Resolution
			break
		}
	}
	return nil
}

func (b *Board) applyDelegation(data []byte) error {
	var p DelegationPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	ask, ok := b.asks[p.AskID]
	if !ok {
		return nil
	}
	ask.Delegates = append(ask.Delegates, p.DelegateSessionID)
	for _, c := range ask.Clauses {
		if c.Source == SourceProposed || c.State == StateRejected {
			continue
		}
		if c.OwnerSessionID == p.DelegatorSessionID || c.OwnerSessionID == "" {
			c.OwnerSessionID = p.DelegateSessionID
		}
	}
	return nil
}

func (b *Board) applySupersession(data []byte) error {
	var p SupersessionPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	c, ok := b.clauses[p.ClauseID]
	if !ok {
		return nil
	}
	c.Text = p.NewVersion
	if p.SourceUtteranceID != "" {
		c.Sources = append(c.Sources, p.SourceUtteranceID)
	}
	return nil
}
