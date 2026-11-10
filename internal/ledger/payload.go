package ledger

type UtterancePayload struct {
	Text        string      `json:"text"`
	UtteranceID string      `json:"utteranceId"`
	SessionID   string      `json:"sessionId"`
	TargetID    string      `json:"targetId"`
	Surface     string      `json:"surface"`
	CapturedBy  string      `json:"capturedBy"`
	TS          string      `json:"ts"`
	Truncated   *Truncation `json:"truncated,omitempty"`
}

type Truncation struct {
	OriginalBytes int    `json:"originalBytes"`
	KeptBytes     int    `json:"keptBytes"`
	SHA256        string `json:"sha256"`
}

type GoalPayload struct {
	GoalID       string `json:"goalId"`
	Name         string `json:"name"`
	ParentGoalID string `json:"parentGoalId,omitempty"`
}

type AskPayload struct {
	AskID           string   `json:"askId"`
	GoalID          string   `json:"goalId,omitempty"`
	Type            AskType  `json:"type"`
	Principal       string   `json:"principal"`
	SourceUtterance string   `json:"sourceUtteranceId"`
	Clauses         []string `json:"clauses"`
}

type ClausePayload struct {
	ClauseID          string       `json:"clauseId"`
	AskID             string       `json:"askId"`
	GoalID            string       `json:"goalId,omitempty"`
	Kind              string       `json:"kind,omitempty"`
	Text              string       `json:"text"`
	State             ClauseState  `json:"state"`
	Source            ClauseSource `json:"source,omitempty"`
	OwnerSessionID    string       `json:"ownerSessionId,omitempty"`
	ProposedBy        string       `json:"proposedBy,omitempty"`
	SourceUtteranceID string       `json:"sourceUtteranceId,omitempty"`
}

type ClauseSourcePayload struct {
	ClauseID       string       `json:"clauseId"`
	FromSource     ClauseSource `json:"fromSource"`
	ToSource       ClauseSource `json:"toSource"`
	UtteranceID    string       `json:"utteranceId"`
	OwnerSessionID string       `json:"ownerSessionId,omitempty"`
}

type AttachPayload struct {
	UtteranceID string   `json:"utteranceId,omitempty"`
	SessionID   string   `json:"sessionId"`
	As          AttachAs `json:"as"`
	To          string   `json:"to,omitempty"`
	GoalID      string   `json:"goalId,omitempty"`
	Effects     []string `json:"effects,omitempty"`
	Note        string   `json:"note,omitempty"`
}

type QuestionPayload struct {
	QuestionID     string       `json:"questionId"`
	Target         string       `json:"target"`
	GoalID         string       `json:"goalId,omitempty"`
	Kind           QuestionKind `json:"kind"`
	Text           string       `json:"text"`
	Recommendation string       `json:"recommendation"`
	DefaultAnswer  string       `json:"defaultAnswer"`
	DeadlineMs     int64        `json:"deadlineMs"`
	KeepsBreadth   bool         `json:"keepsBreadth,omitempty"`
	OwnerSessionID string       `json:"ownerSessionId"`
}

type QuestionResolvedPayload struct {
	QuestionID        string   `json:"questionId"`
	Answer            string   `json:"answer"`
	SourceUtteranceID string   `json:"sourceUtteranceId,omitempty"`
	Applied           bool     `json:"applied"`
	Auto              bool     `json:"auto,omitempty"`
	Effects           []string `json:"effects,omitempty"`
}

type QuestionEscalatedPayload struct {
	QuestionID string `json:"questionId"`
	DeadlineMs int64  `json:"deadlineMs"`
}

type WorkItemPayload struct {
	ItemID    string `json:"itemId"`
	ClauseID  string `json:"clauseId,omitempty"`
	Text      string `json:"text"`
	CreatedBy string `json:"createdBy"`
	WorkFields
	ImportedFrom string       `json:"importedFrom,omitempty"`
	History      *WorkHistory `json:"history,omitempty"`
}

type WorkItemStatePayload struct {
	ItemID      string    `json:"itemId"`
	FromState   WorkState `json:"fromState"`
	ToState     WorkState `json:"toState"`
	Actor       string    `json:"actor"`
	Assignee    string    `json:"assignee,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	Reviewer    string    `json:"reviewer,omitempty"`
	UtteranceID string    `json:"utteranceId,omitempty"`
	Handoff     *Handoff  `json:"handoff,omitempty"`
}

type DelegationPayload struct {
	AskID              string `json:"askId"`
	DelegatorSessionID string `json:"delegatorSessionId"`
	DelegateSessionID  string `json:"delegateSessionId"`
	SourceUtteranceID  string `json:"sourceUtteranceId,omitempty"`
}

type SupersessionPayload struct {
	ClauseID          string `json:"clauseId"`
	OldVersion        string `json:"oldVersion"`
	NewVersion        string `json:"newVersion"`
	SourceUtteranceID string `json:"sourceUtteranceId"`
}

type ClauseStatePayload struct {
	ClauseID       string      `json:"clauseId"`
	FromState      ClauseState `json:"fromState"`
	ToState        ClauseState `json:"toState"`
	EvidenceRef    string      `json:"evidenceRef,omitempty"`
	ProgressRef    string      `json:"progressRef,omitempty"`
	OwnerSessionID string      `json:"ownerSessionId,omitempty"`
	Actor          string      `json:"actor"`
}

type GatePayload struct {
	Gate      string   `json:"gate"`
	SessionID string   `json:"sessionId"`
	TurnID    string   `json:"turnId,omitempty"`
	Result    string   `json:"result"`
	Blocked   []string `json:"blocked,omitempty"`
	Waived    []string `json:"waived,omitempty"`
}

type CheckPayload struct {
	Gate       string   `json:"gate"`
	Run        string   `json:"run"`
	GoalID     string   `json:"goalId"`
	SessionID  string   `json:"sessionId"`
	Result     string   `json:"result"`
	Utterances []string `json:"utterances,omitempty"`
	Cuts       []string `json:"cuts,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

type CutWithdrawnPayload struct {
	CutID       string   `json:"cutId"`
	ClauseIDs   []string `json:"clauseIds"`
	UtteranceID string   `json:"utteranceId"`
	SessionID   string   `json:"sessionId"`
}

type DecisionPayload struct {
	DecisionID      string `json:"decisionId"`
	GoalID          string `json:"goalId,omitempty"`
	QuestionID      string `json:"questionId,omitempty"`
	Version         int    `json:"version"`
	Text            string `json:"text"`
	Principal       string `json:"principal"`
	SourceUtterance string `json:"sourceUtteranceId"`
}

type CutPayload struct {
	CutID         string `json:"cutId"`
	ClauseID      string `json:"clauseId,omitempty"`
	Target        string `json:"target,omitempty"`
	QuestionID    string `json:"questionId,omitempty"`
	Reason        string `json:"reason"`
	AuthorSession string `json:"authorSessionId"`
	Acked         bool   `json:"acked"`
	CheckRun      string `json:"checkRun,omitempty"`
	FromUtterance string `json:"fromUtterance,omitempty"`
}

type CutAckPayload struct {
	CutID             string `json:"cutId"`
	AckedBy           string `json:"ackedBy"`
	SourceUtteranceID string `json:"sourceUtteranceId,omitempty"`
}

type WaitPayload struct {
	ClauseID   string `json:"clauseId"`
	SessionID  string `json:"sessionId"`
	Condition  string `json:"condition"`
	DeadlineMs int64  `json:"deadlineMs"`
}

type WaitResolvedPayload struct {
	ClauseID   string `json:"clauseId"`
	SessionID  string `json:"sessionId"`
	Resolution string `json:"resolution"`
}

type EvidencePayload struct {
	EvidenceID  string `json:"evidenceId"`
	ClauseID    string `json:"clauseId"`
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	BoundCommit string `json:"boundCommit"`
}

type WaiverPayload struct {
	Gate      string `json:"gate"`
	ClauseID  string `json:"clauseId"`
	Reason    string `json:"reason"`
	SessionID string `json:"sessionId"`
}

type RedactPayload struct {
	TargetUtteranceID string `json:"targetUtteranceId"`
	RedactedBy        string `json:"redactedBy"`
}
