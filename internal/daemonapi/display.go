package daemonapi

import (
	"fmt"
	"time"
)

type SessionDisplay struct {
	Kind          string     `json:"kind"`
	Label         string     `json:"label"`
	Detail        string     `json:"detail"`
	WaitingPlace  string     `json:"waitingPlace,omitempty"`
	ResetsAt      *time.Time `json:"resetsAt,omitempty"`
	ResumesAt     *time.Time `json:"resumesAt,omitempty"`
	QuietSince    *time.Time `json:"quietSince,omitempty"`
	ToolStartedAt *time.Time `json:"toolStartedAt,omitempty"`
}

type SessionAttention struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// AttentionReplacedRequest is an open request of a conversation that is no
// longer its project's lead; it is shown apart and does not count as needing the person.
const AttentionReplacedRequest = "replacedRequest"

func CountText(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
