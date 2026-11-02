package ledger

import "time"

type EventType string

const (
	EventUtterance    EventType = "utterance"
	EventAsk          EventType = "ask"
	EventClause       EventType = "clause"
	EventClauseState  EventType = "clause.state"
	EventDecision     EventType = "decision"
	EventDecisionVer  EventType = "decision.version"
	EventCut          EventType = "cut"
	EventCutAck       EventType = "cut.ack"
	EventWait         EventType = "wait"
	EventWaitResolved EventType = "wait.resolved"
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
)

type AskType string

const (
	AskBuild    AskType = "build"
	AskInquiry  AskType = "inquiry"
	AskDecision AskType = "decision"
)

type ClauseState string

const (
	StateCaptured    ClauseState = "captured"
	StateReadBack    ClauseState = "read-back"
	StateOwned       ClauseState = "owned"
	StateBriefed     ClauseState = "briefed"
	StateBuilt       ClauseState = "built"
	StateConnected   ClauseState = "connected"
	StateMerged      ClauseState = "merged"
	StateVerifiedLive ClauseState = "verified-live"
	StateReported    ClauseState = "reported"
)

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
	CreatedAt      time.Time
}
