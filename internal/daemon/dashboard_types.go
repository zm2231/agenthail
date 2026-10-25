package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

type dashboardSurface struct {
	Name         string                 `json:"name"`
	Connected    bool                   `json:"connected"`
	Error        string                 `json:"error,omitempty"`
	Health       string                 `json:"health"`
	HealthDetail string                 `json:"healthDetail,omitempty"`
	Runtime      *surface.RuntimeStatus `json:"runtime,omitempty"`
	RepairAction string                 `json:"repairAction,omitempty"`
	RepairLabel  string                 `json:"repairLabel,omitempty"`
	Capabilities surface.Capabilities   `json:"capabilities"`
}

type dashboardSession struct {
	ID                  string                     `json:"id"`
	Surface             surface.SurfaceKind        `json:"surface"`
	RuntimeKind         surface.RuntimeKind        `json:"runtimeKind,omitempty"`
	ConfiguredModel     string                     `json:"configuredModel,omitempty"`
	PendingModel        string                     `json:"pendingModel,omitempty"`
	ClaudeConfigRoot    *surface.SessionConfigRoot `json:"claudeConfigRoot,omitempty"`
	Name                string                     `json:"name"`
	Cwd                 string                     `json:"cwd,omitempty"`
	Alias               string                     `json:"alias,omitempty"`
	Status              surface.SessionStatus      `json:"status"`
	WaitingOn           *surface.Waiting           `json:"waitingOn,omitempty"`
	BackgroundTasks     *surface.BackgroundTasks   `json:"backgroundTasks,omitempty"`
	UsageLimit          *surface.UsageLimit        `json:"usageLimit,omitempty"`
	LimitResume         *dashboardLimitResume      `json:"limitResume,omitempty"`
	Activity            *surface.Activity          `json:"activity,omitempty"`
	Plan                *surface.Plan              `json:"plan,omitempty"`
	LastActive          time.Time                  `json:"lastActive,omitempty"`
	QueueCount          int                        `json:"queueCount"`
	Open                bool                       `json:"open"`
	Current             bool                       `json:"current"`
	Archived            bool                       `json:"archived,omitempty"`
	CurrentReason       string                     `json:"currentReason,omitempty"`
	Capabilities        surface.Capabilities       `json:"capabilities"`
	ReadOnly            bool                       `json:"readOnly,omitempty"`
	ReadOnlyReason      string                     `json:"readOnlyReason,omitempty"`
	Source              string                     `json:"source,omitempty"`
	Transport           string                     `json:"transport,omitempty"`
	ForkedFrom          string                     `json:"forkedFrom,omitempty"`
	Project             *CatalogProject            `json:"project,omitempty"`
	Checkout            *catalogCheckout           `json:"checkout,omitempty"`
	ObservedAt          time.Time                  `json:"observedAt,omitempty"`
	UnavailableReason   string                     `json:"unavailableReason,omitempty"`
	Runtime             *surface.Runtime           `json:"runtime,omitempty"`
	Freshness           *registry.CatalogFreshness `json:"freshness,omitempty"`
	Subagent            *surface.Subagent          `json:"subagent,omitempty"`
	Delegated           bool                       `json:"delegated,omitempty"`
	Subagents           *surface.SubagentRollup    `json:"subagents,omitempty"`
	Holders             []surface.Holder           `json:"holders,omitempty"`
	ObservedSubagents   *surface.SubagentRollup    `json:"observedSubagents,omitempty"`
	HiddenSubagents     *surface.SubagentRollup    `json:"hiddenSubagents,omitempty"`
	LiveHiddenSubagents *surface.SubagentRollup    `json:"liveHiddenSubagents,omitempty"`
	Listed              bool                       `json:"listed"`
	Host                *dashboardHost             `json:"host,omitempty"`
}

type dashboardPerson struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

type dashboardState struct {
	UpdatedAt          time.Time                   `json:"updatedAt"`
	EventCursor        uint64                      `json:"eventCursor"`
	HostEpoch          string                      `json:"hostEpoch"`
	CatalogSeq         uint64                      `json:"catalogSeq"`
	Daemon             map[string]any              `json:"daemon"`
	Surfaces           []dashboardSurface          `json:"surfaces"`
	Sessions           []dashboardSession          `json:"sessions"`
	TotalSessions      int                         `json:"totalSessions"`
	Counts             map[string]int              `json:"counts,omitempty"`
	Projects           []dashboardProject          `json:"projects,omitempty"`
	NextCursor         string                      `json:"nextCursor,omitempty"`
	Queue              []dashboardQueue            `json:"queue"`
	Channels           []dashboardChannel          `json:"channels"`
	Relays             []dashboardRelay            `json:"relays"`
	History            []dashboardHistory          `json:"history"`
	Attention          []dashboardAttention        `json:"attention"`
	DeliveryProblems   []registry.DeliveryProblem  `json:"deliveryProblems"`
	CodexRecentHours   int                         `json:"codexRecentHours"`
	BusyDelivery       string                      `json:"busyDelivery"`
	Signin             []signin.Status             `json:"signin"`
	Rekeyed            map[string]string           `json:"rekeyed,omitempty"`
	Coordinators       []registry.CoordinatorEntry `json:"coordinators"`
	Person             dashboardPerson             `json:"person"`
	catalogPageApplied bool
	catalogPageFilter  string
	catalogPageOffset  int
	catalogPageLimit   int
	catalogPageHasMore bool
}

type dashboardAttention struct {
	ID              int64  `json:"id"`
	SessionID       string `json:"sessionId"`
	Target          string `json:"target"`
	QueueID         int64  `json:"queueId"`
	Reason          string `json:"reason"`
	RequestedAction string `json:"requestedAction"`
	CreatedAt       string `json:"createdAt"`
}

type dashboardQueue struct {
	surface.TurnOptions
	SourceSessionID string                   `json:"sourceSessionId,omitempty"`
	ID              int64                    `json:"id"`
	SessionID       string                   `json:"sessionId"`
	Target          string                   `json:"target"`
	Message         string                   `json:"message"`
	Model           string                   `json:"model,omitempty"`
	Status          string                   `json:"status"`
	Attempts        int                      `json:"attempts"`
	LastError       string                   `json:"lastError,omitempty"`
	QueuedAt        string                   `json:"queuedAt"`
	HeldBy          registry.QueueHold       `json:"heldBy,omitempty"`
	Historical      bool                     `json:"historical"`
	Evidence        surface.DeliveryEvidence `json:"evidence"`
	Operation       registry.QueueOperation  `json:"operation"`
	BusyDelivery    string                   `json:"busyDelivery,omitempty"`
}

type dashboardChannel struct {
	Name          string                   `json:"name"`
	Members       []string                 `json:"members"`
	MemberDetails []dashboardChannelMember `json:"memberDetails"`
}

type dashboardChannelMember struct {
	ID      string `json:"id"`
	Display string `json:"display"`
}

type dashboardRelay struct {
	Handoff      string `json:"handoff"`
	EventCursor  int64  `json:"eventCursor"`
	PendingCount int64  `json:"pendingCount"`
	LastError    string `json:"lastError,omitempty"`
	ID           int64  `json:"id"`
	From         string `json:"from"`
	To           string `json:"to"`
	Pattern      string `json:"pattern"`
	Once         bool   `json:"once"`
	Active       bool   `json:"active"`
	FireCount    int64  `json:"fireCount"`
	LastFiredAt  string `json:"lastFiredAt,omitempty"`
}

type dashboardHistory struct {
	ID              int64                    `json:"id"`
	CreatedAt       string                   `json:"createdAt"`
	Kind            string                   `json:"kind"`
	SessionID       string                   `json:"sessionId,omitempty"`
	SourceSessionID string                   `json:"sourceSessionId,omitempty"`
	Target          string                   `json:"target,omitempty"`
	Source          string                   `json:"source,omitempty"`
	QueueID         int64                    `json:"queueId,omitempty"`
	Message         string                   `json:"message,omitempty"`
	Result          string                   `json:"result,omitempty"`
	Error           string                   `json:"error,omitempty"`
	Evidence        surface.DeliveryEvidence `json:"evidence,omitempty"`
	Operation       registry.QueueOperation  `json:"operation,omitempty"`
}
