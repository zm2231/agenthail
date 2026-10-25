package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type dashboardActionRequest struct {
	surface.TurnOptions
	Name            string                     `json:"name"`
	Worktree        string                     `json:"worktree"`
	Agent           string                     `json:"agent"`
	PermissionMode  string                     `json:"permissionMode"`
	ConfigRoot      string                     `json:"configRoot"`
	StopTerminal    bool                       `json:"stopTerminal"`
	Fork            surface.ForkOptions        `json:"fork"`
	NativeQueue     surface.NativeQueueRequest `json:"nativeQueue"`
	Action          string                     `json:"action"`
	SourceSessionID string                     `json:"sourceSessionId"`
	SessionID       string                     `json:"sessionId"`
	Message         string                     `json:"message"`
	Alias           string                     `json:"alias"`
	Model           string                     `json:"model"`
	BusyDelivery    string                     `json:"busyDelivery"`
	QueueID         int64                      `json:"queueId"`
	Channel         string                     `json:"channel"`
	NewChannel      string                     `json:"newChannel"`
	TargetID        string                     `json:"targetId"`
	FromID          string                     `json:"fromId"`
	ToID            string                     `json:"toId"`
	Pattern         string                     `json:"pattern"`
	Handoff         string                     `json:"handoff"`
	Once            bool                       `json:"once"`
	RelayID         int64                      `json:"relayId"`
	RelayHops       int                        `json:"relayHops"`
	DeliveryID      int64                      `json:"deliveryId"`
	Surface         string                     `json:"surface"`
	Cwd             string                     `json:"cwd"`
	Launcher        string                     `json:"launcher"`
	Approval        string                     `json:"approvalPolicy"`
	Author          surface.Author             `json:"author"`
	PersonName      string                     `json:"personName"`
	PID             int                        `json:"pid"`
	PIDStartedAt    int64                      `json:"pidStartedAt"`
	ProjectID       string                     `json:"projectId"`
	Path            string                     `json:"path"`
	Title           string                     `json:"title"`
	Attachments     []string                   `json:"attachments"`
	RequestID       string                     `json:"requestId"`
	Decision        surface.Decision           `json:"decision"`
	Instructions    *string                    `json:"instructions"`
	Replace         bool                       `json:"replace"`
	ChangedAt       time.Time                  `json:"changedAt"`
	ChangedBy       string                     `json:"changedBy"`
}
