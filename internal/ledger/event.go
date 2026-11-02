package ledger

import "time"

type EventType string

const (
	EventUtterance     EventType = "utterance"
	EventAsk           EventType = "ask"
	EventClause        EventType = "clause"
	EventClauseState   EventType = "clause.state"
	EventDecision      EventType = "decision"
	EventDecisionVer   EventType = "decision.version"
	EventCut           EventType = "cut"
	EventCutAck        EventType = "cut.ack"
	EventWait          EventType = "wait"
	EventWaitResolved  EventType = "wait.resolved"
	EventEvidence      EventType = "evidence"
	EventGoal          EventType = "goal"
	EventDelegation    EventType = "delegation"
	EventSupersession  EventType = "supersession"
	EventWorkItem      EventType = "work_item"
	EventWorkItemState EventType = "work_item.state"
	EventQuestion      EventType = "question"
	EventQuestionRes   EventType = "question.resolved"
	EventCorrection    EventType = "correction"
	EventWaiver        EventType = "waiver"
	EventRedact        EventType = "redact"
	EventAttach        EventType = "attach"
	EventClauseSource  EventType = "clause.source"
	EventQuestionEsc   EventType = "question.escalated"
	EventGate          EventType = "gate"
	EventWorkItemDep   EventType = "work_item.dep"
	EventWorkComment   EventType = "work_item.comment"
	EventWorkLog       EventType = "work_item.log"
	EventCutWithdrawn  EventType = "cut.withdrawn"
	EventReclassified  EventType = "utterance.reclassified"
)

const (
	GateG2    = "G2"
	GateCheck = "G-check"

	CheckStarted     = "started"
	CheckReported    = "reported"
	CheckNone        = "none"
	CheckTimedOut    = "timed-out"
	CheckInterrupted = "interrupted"
	CheckFailed      = "failed"

	GatePass           = "pass"
	GateBlocked        = "blocked"
	GateWaived         = "waived"
	GateDeferredPerson = "deferred-person-active"
)

type QuestionKind string

const (
	QuestionScope   QuestionKind = "scope"
	QuestionFraming QuestionKind = "framing"
	QuestionOutward QuestionKind = "outward"
)

const OutwardDefault = "don't"

const PrincipalDefault = "default"

type AskType string

const (
	AskBuild    AskType = "build"
	AskInquiry  AskType = "inquiry"
	AskDecision AskType = "decision"
)

type ClauseState string

const (
	StateCaptured     ClauseState = "captured"
	StateReadBack     ClauseState = "read-back"
	StateOwned        ClauseState = "owned"
	StateBriefed      ClauseState = "briefed"
	StateBuilt        ClauseState = "built"
	StateConnected    ClauseState = "connected"
	StateMerged       ClauseState = "merged"
	StateVerifiedLive ClauseState = "verified-live"
	StateReported     ClauseState = "reported"
	StateRejected     ClauseState = "rejected"
)

const ClauseKindConstraint = "constraint"

type ClauseSource string

const (
	SourceStated    ClauseSource = "stated"
	SourceProposed  ClauseSource = "proposed"
	SourceAdopted   ClauseSource = "adopted"
	SourceDelegated ClauseSource = "delegated"
	SourceInferred  ClauseSource = "inferred"
)

type Event struct {
	Seq            int64
	ProjectKey     string
	Type           EventType
	Payload        []byte
	ActorSessionID string
	ActorHostID    string
	OwnerSeq       int64
	OriginSeq      int64
	Ref            string
	CreatedAt      time.Time
}
