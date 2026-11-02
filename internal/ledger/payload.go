package ledger

type UtterancePayload struct {
	Text      string `json:"text"`
	MessageID string `json:"messageId"`
	SessionID string `json:"sessionId"`
	TargetID  string `json:"targetId"`
	Surface   string `json:"surface"`
	TS        string `json:"ts"`
}

type AskPayload struct {
	AskID            string   `json:"askId"`
	Type             AskType  `json:"type"`
	Principal        string   `json:"principal"`
	SourceUtterance  string   `json:"sourceUtteranceId"`
	Clauses          []string `json:"clauses"`
}

type ClausePayload struct {
	ClauseID       string      `json:"clauseId"`
	AskID          string      `json:"askId"`
	Text           string      `json:"text"`
	State          ClauseState `json:"state"`
	OwnerSessionID string      `json:"ownerSessionId,omitempty"`
}

type ClauseStatePayload struct {
	ClauseID    string      `json:"clauseId"`
	FromState   ClauseState `json:"fromState"`
	ToState     ClauseState `json:"toState"`
	EvidenceRef string      `json:"evidenceRef,omitempty"`
	ProgressRef string      `json:"progressRef,omitempty"`
	Actor       string      `json:"actor"`
}

type DecisionPayload struct {
	DecisionID      string `json:"decisionId"`
	Version         int    `json:"version"`
	Text            string `json:"text"`
	Principal       string `json:"principal"`
	SourceUtterance string `json:"sourceUtteranceId"`
}

type CutPayload struct {
	CutID          string `json:"cutId"`
	ClauseID       string `json:"clauseId"`
	Reason         string `json:"reason"`
	AuthorSession  string `json:"authorSessionId"`
	Acked          bool   `json:"acked"`
}

type CutAckPayload struct {
	CutID   string `json:"cutId"`
	AckedBy string `json:"ackedBy"`
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
