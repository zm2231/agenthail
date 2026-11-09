package triggers

import (
	"encoding/json"
	"time"
)

const (
	MaxPayloadBytes = 16 << 10
	maxNameBytes    = 200
	maxPathBytes    = 4096
	maxIDBytes      = 512
)

type Completion struct {
	HostID      string
	HostName    string
	SessionID   string
	Alias       string
	Surface     string
	Name        string
	Cwd         string
	ProjectID   string
	ProjectName string
	TurnID      string
	Outcome     string
	CompletedAt time.Time
	Delegated   bool
}

func (c Completion) DedupeKey() string {
	return c.HostID + ":" + c.SessionID + ":" + c.TurnID
}

type payload struct {
	Version     int            `json:"version"`
	Event       string         `json:"event"`
	DedupeKey   string         `json:"dedupeKey"`
	Trigger     string         `json:"trigger"`
	Test        bool           `json:"test,omitempty"`
	Host        payloadHost    `json:"host"`
	Session     payloadSession `json:"session"`
	Project     *payloadProj   `json:"project,omitempty"`
	TurnID      string         `json:"turnId"`
	Outcome     string         `json:"outcome"`
	CompletedAt string         `json:"completedAt"`
	Truncated   bool           `json:"truncated,omitempty"`
}

type payloadHost struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type payloadSession struct {
	ID      string `json:"id"`
	Alias   string `json:"alias,omitempty"`
	Surface string `json:"surface"`
	Name    string `json:"name,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
}

type payloadProj struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Payload is the JSON a run trigger reads on stdin, at most MaxPayloadBytes.
func Payload(c Completion, trigger string, test bool) []byte {
	truncated := false
	cut := func(value string, limit int) string {
		if len(value) <= limit {
			return value
		}
		truncated = true
		for limit > 0 && value[limit]&0xC0 == 0x80 {
			limit--
		}
		return value[:limit]
	}
	document := payload{
		Version:     1,
		Event:       "turn.completed",
		DedupeKey:   cut(c.DedupeKey(), 3*maxIDBytes),
		Trigger:     trigger,
		Test:        test,
		Host:        payloadHost{ID: cut(c.HostID, maxIDBytes), Name: cut(c.HostName, maxNameBytes)},
		Session:     payloadSession{ID: cut(c.SessionID, maxIDBytes), Alias: cut(c.Alias, maxNameBytes), Surface: cut(c.Surface, maxNameBytes), Name: cut(c.Name, maxNameBytes), Cwd: cut(c.Cwd, maxPathBytes)},
		TurnID:      cut(c.TurnID, maxIDBytes),
		Outcome:     c.Outcome,
		CompletedAt: c.CompletedAt.UTC().Format(time.RFC3339),
	}
	if c.ProjectID != "" {
		document.Project = &payloadProj{ID: cut(c.ProjectID, maxIDBytes), Name: cut(c.ProjectName, maxNameBytes)}
	}
	document.Truncated = truncated
	data, _ := json.Marshal(document)
	if len(data) > MaxPayloadBytes {
		document.Session.Cwd, document.Session.Name, document.Truncated = "", "", true
		data, _ = json.Marshal(document)
	}
	return data
}
