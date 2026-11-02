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
	EventEvidence     EventType = "evidence"
	EventCorrection   EventType = "correction"
	EventWaiver       EventType = "waiver"
	EventRedact       EventType = "redact"
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
