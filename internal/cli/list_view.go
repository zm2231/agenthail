package cli

import (
	"encoding/json"
	"maps"

	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// The list types read only what list renders; each keeps the daemon's JSON so --json re-emits every field.
type listSnapshot struct {
	View       *listView     `json:"view"`
	Sessions   []listSession `json:"sessions"`
	NextCursor string        `json:"nextCursor,omitempty"`
	rest       map[string]json.RawMessage
}

type listView struct {
	Revision string               `json:"revision"`
	Scope    string               `json:"scope"`
	Counts   map[string]listCount `json:"counts"`
	Projects []listProject        `json:"projects"`
	Families []listFamily         `json:"families"`
	rest     map[string]json.RawMessage
}

type listCount struct {
	Families int `json:"families"`
	Sessions int `json:"sessions"`
}

type listProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type listFamily struct {
	RootID           string       `json:"rootId"`
	WorkingSubagents int          `json:"workingSubagents"`
	Members          []listMember `json:"members"`
	raw              json.RawMessage
}

type listMember struct {
	ID          string `json:"id"`
	Depth       int    `json:"depth"`
	ContextOnly bool   `json:"contextOnly"`
}

type listSession struct {
	ID               string                       `json:"id"`
	Surface          string                       `json:"surface"`
	Name             string                       `json:"name"`
	Alias            string                       `json:"alias"`
	Cwd              string                       `json:"cwd"`
	QueueCount       int                          `json:"queueCount"`
	Display          *daemonapi.SessionDisplay    `json:"display"`
	Subagent         *surface.Subagent            `json:"subagent"`
	Holders          []surface.Holder             `json:"holders"`
	ClaudeConfigRoot *surface.SessionConfigRoot   `json:"claudeConfigRoot"`
	ReplacedLead     []registry.CoordinatorTenure `json:"replacedLead"`
	Host             *struct {
		Name string `json:"name"`
	} `json:"host"`
	Project *struct {
		DisplayName string `json:"displayName"`
	} `json:"project"`
	raw json.RawMessage
}

func (s *listSnapshot) UnmarshalJSON(data []byte) error {
	type plain listSnapshot
	rest, err := decodeKeeping(data, (*plain)(s))
	s.rest = rest
	return err
}

func (s listSnapshot) MarshalJSON() ([]byte, error) {
	fields := map[string]any{"view": s.View, "sessions": s.Sessions}
	if s.NextCursor != "" {
		fields["nextCursor"] = s.NextCursor
	}
	return encodeOver(s.rest, []string{"nextCursor"}, fields)
}

func (v *listView) UnmarshalJSON(data []byte) error {
	type plain listView
	rest, err := decodeKeeping(data, (*plain)(v))
	v.rest = rest
	return err
}

func (v listView) MarshalJSON() ([]byte, error) {
	return encodeOver(v.rest, nil, map[string]any{"families": v.Families})
}

func (f *listFamily) UnmarshalJSON(data []byte) error {
	type plain listFamily
	f.raw = append(json.RawMessage(nil), data...)
	return json.Unmarshal(data, (*plain)(f))
}

func (f listFamily) MarshalJSON() ([]byte, error) {
	type plain listFamily
	return rawOr(f.raw, plain(f))
}

func (s *listSession) UnmarshalJSON(data []byte) error {
	type plain listSession
	s.raw = append(json.RawMessage(nil), data...)
	return json.Unmarshal(data, (*plain)(s))
}

func (s listSession) MarshalJSON() ([]byte, error) {
	type plain listSession
	return rawOr(s.raw, plain(s))
}

func decodeKeeping(data []byte, into any) (map[string]json.RawMessage, error) {
	if err := json.Unmarshal(data, into); err != nil {
		return nil, err
	}
	var rest map[string]json.RawMessage
	return rest, json.Unmarshal(data, &rest)
}

func encodeOver(rest map[string]json.RawMessage, drop []string, fields map[string]any) ([]byte, error) {
	out := maps.Clone(rest)
	if out == nil {
		out = map[string]json.RawMessage{}
	}
	for _, key := range drop {
		delete(out, key)
	}
	for key, value := range fields {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		out[key] = encoded
	}
	return json.Marshal(out)
}

func rawOr(raw json.RawMessage, value any) ([]byte, error) {
	if raw != nil {
		return raw, nil
	}
	return json.Marshal(value)
}
