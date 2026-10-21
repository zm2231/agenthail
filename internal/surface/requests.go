package surface

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrRequestStale means the request was already answered or withdrawn, or
// its session's process has ended, so no answer can reach it.
var ErrRequestStale = errors.New("this request was already answered or has expired")

// Request is something an open turn asks its user and waits on: a tool to
// approve, questions to answer, or an MCP server's request for input. ID is
// the provider's own id for it, which is also the session's Waiting ID while
// it is the oldest open request.
type Request struct {
	ID          string          `json:"id"`
	Kind        WaitingKind     `json:"kind"`
	Tool        string          `json:"tool"`
	Title       string          `json:"title,omitempty"`
	DisplayName string          `json:"displayName,omitempty"`
	Description string          `json:"description,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	BlockedPath string          `json:"blockedPath,omitempty"`
	AgentID     string          `json:"agentId,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
	Questions   []Question      `json:"questions,omitempty"`
	Elicitation *Elicitation    `json:"elicitation,omitempty"`
	AlwaysAllow bool            `json:"alwaysAllow"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type Question struct {
	Question    string           `json:"question"`
	Header      string           `json:"header"`
	MultiSelect bool             `json:"multiSelect"`
	Options     []QuestionOption `json:"options"`
}

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Elicitation is what an MCP server asks of an input request: a form of flat
// fields, or in url mode a link for the person to open.
type Elicitation struct {
	Server  string             `json:"server"`
	Message string             `json:"message"`
	Mode    string             `json:"mode"`
	URL     string             `json:"url,omitempty"`
	Fields  []ElicitationField `json:"fields,omitempty"`
}

// ElicitationField is one form field. Type is "string", "number", "integer"
// or "boolean"; a string with Options takes one of their values, or with
// MultiSelect a list of them.
type ElicitationField struct {
	Name        string              `json:"name"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Type        string              `json:"type"`
	Required    bool                `json:"required"`
	MultiSelect bool                `json:"multiSelect"`
	Options     []ElicitationOption `json:"options,omitempty"`
	Default     json.RawMessage     `json:"default,omitempty"`
}

type ElicitationOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Decision is a person's answer to a Request. Behavior is "allow" or "deny".
// Always allows the tool from now on with the rule the request offers. A
// question is allowed with Answers, one per question keyed by its text; a
// multi-select answer joins its labels with ", ". An input request is allowed
// with Content, an object of its form's field values, or none for a link.
// Choice is instead a notification button's id, which the daemon resolves
// with Request.Choose against the open request.
type Decision struct {
	Choice   string            `json:"choice,omitempty"`
	Behavior string            `json:"behavior"`
	Always   bool              `json:"always,omitempty"`
	Message  string            `json:"message,omitempty"`
	Answers  map[string]string `json:"answers,omitempty"`
	Content  json.RawMessage   `json:"content,omitempty"`
}

// RequestState is a session's open requests, oldest first, and the
// permission mode it runs in, empty when it runs in its settings' default.
type RequestState struct {
	PermissionMode string    `json:"permissionMode"`
	Requests       []Request `json:"requests"`
}

// Responder is implemented by an adapter whose sessions' requests a person
// answers through Agenthail. Respond returns how the provider resolved the
// request, such as "allowed", "denied" or "answered".
type Responder interface {
	Requests(ctx context.Context, session *Session) (RequestState, error)
	Respond(ctx context.Context, session *Session, requestID string, decision Decision) (string, error)
}
