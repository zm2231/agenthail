package surface

import (
	"bytes"
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
	// AlwaysDeny offers a rule that denies the request from now on, Stoppable
	// a denial that also ends the turn, and NoReason says a denial carries no
	// message to the agent. OptionsOnly takes only its options' ids, never
	// typed text.
	AlwaysDeny  bool      `json:"alwaysDeny,omitempty"`
	Stoppable   bool      `json:"stoppable,omitempty"`
	NoReason    bool      `json:"noReason,omitempty"`
	OptionsOnly bool      `json:"optionsOnly,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	Deadline    time.Time `json:"deadline,omitzero"`
}

type Question struct {
	Question    string           `json:"question"`
	Header      string           `json:"header"`
	MultiSelect bool             `json:"multiSelect"`
	Options     []QuestionOption `json:"options"`
	Suggested   *int             `json:"suggested,omitempty"`
	Links       []QuestionLink   `json:"links,omitempty"`
}

type QuestionLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type QuestionOption struct {
	ID          string `json:"id"`
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
// Always allows the tool from now on with the rule the request offers, or
// with deny denies it from now on. Stop with deny also ends the turn. A
// question is allowed with Answers, one per question keyed by its text; a
// multi-select answer joins its labels with ", ". An input request is allowed
// with Content, an object of its form's field values, or none for a link.
type Decision struct {
	Behavior string            `json:"behavior"`
	Always   bool              `json:"always,omitempty"`
	Stop     bool              `json:"stop,omitempty"`
	Message  string            `json:"message,omitempty"`
	Answers  map[string]string `json:"answers,omitempty"`
	Content  json.RawMessage   `json:"content,omitempty"`
}

// Response is how a client answers any request. Choices are ids the
// request lists: its actions, and its questions' options, at most one per
// single-choice question. Answers hold typed answers keyed by question text,
// Message a note or a denial's reason, and Content an input form's field
// values.
type Response struct {
	Choices []string          `json:"choices,omitempty"`
	Answers map[string]string `json:"answers,omitempty"`
	Message string            `json:"message,omitempty"`
	Content json.RawMessage   `json:"content,omitempty"`
}

func (r *Response) UnmarshalJSON(data []byte) error {
	type fields Response
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode((*fields)(r))
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
