package acp

import "encoding/json"

const ProtocolVersion = 1

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type FSCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type ClientCapabilities struct {
	FS       FSCapabilities `json:"fs"`
	Terminal bool           `json:"terminal"`
}

type InitializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         ClientInfo         `json:"clientInfo"`
}

type MCPCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

type AgentCapabilities struct {
	LoadSession         bool                       `json:"loadSession"`
	MCPCapabilities     MCPCapabilities            `json:"mcpCapabilities"`
	SessionCapabilities map[string]json.RawMessage `json:"sessionCapabilities"`
}

func (c AgentCapabilities) Session(name string) bool {
	_, ok := c.SessionCapabilities[name]
	return ok
}

type AgentInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AgentInfo         AgentInfo         `json:"agentInfo"`
}

type NameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type MCPServer struct {
	Type    string      `json:"type,omitempty"`
	Name    string      `json:"name"`
	URL     string      `json:"url,omitempty"`
	Headers []NameValue `json:"headers,omitempty"`
	Command string      `json:"command,omitempty"`
	Args    []string    `json:"args,omitempty"`
	Env     []NameValue `json:"env,omitempty"`
}

type NewSessionParams struct {
	Cwd        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers"`
}

type LoadSessionParams struct {
	SessionID  string      `json:"sessionId"`
	Cwd        string      `json:"cwd"`
	MCPServers []MCPServer `json:"mcpServers"`
}

type Mode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type Modes struct {
	CurrentModeID  string `json:"currentModeId"`
	AvailableModes []Mode `json:"availableModes"`
}

type Model struct {
	ModelID string `json:"modelId"`
	Name    string `json:"name"`
}

type Models struct {
	CurrentModelID  string  `json:"currentModelId"`
	AvailableModels []Model `json:"availableModels"`
}

type ConfigValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ConfigOption struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Category     string        `json:"category"`
	Type         string        `json:"type"`
	CurrentValue string        `json:"currentValue"`
	Options      []ConfigValue `json:"options"`
}

type SessionSetup struct {
	SessionID     string         `json:"sessionId,omitempty"`
	Modes         *Modes         `json:"modes,omitempty"`
	Models        *Models        `json:"models,omitempty"`
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type PromptResult struct {
	StopReason string `json:"stopReason"`
}

type SessionIDParams struct {
	SessionID string `json:"sessionId"`
}

type SetConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

type SetConfigOptionResult struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

type SetModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

type SetModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

type SessionNotification struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

type ToolCallContent struct {
	Type    string        `json:"type"`
	Content *ContentBlock `json:"content,omitempty"`
	Path    string        `json:"path,omitempty"`
	OldText *string       `json:"oldText,omitempty"`
	NewText string        `json:"newText,omitempty"`
}

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status"`
}

type Update struct {
	SessionUpdate string            `json:"sessionUpdate"`
	Content       json.RawMessage   `json:"content,omitempty"`
	MessageID     string            `json:"messageId,omitempty"`
	ToolCallID    string            `json:"toolCallId,omitempty"`
	Title         *string           `json:"title,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Status        string            `json:"status,omitempty"`
	RawInput      json.RawMessage   `json:"rawInput,omitempty"`
	RawOutput     json.RawMessage   `json:"rawOutput,omitempty"`
	Entries       []PlanEntry       `json:"entries,omitempty"`
	CurrentModeID string            `json:"currentModeId,omitempty"`
	ConfigOptions []ConfigOption    `json:"configOptions,omitempty"`
	Used          *int64            `json:"used,omitempty"`
	Size          *int64            `json:"size,omitempty"`
	Commands      []json.RawMessage `json:"availableCommands,omitempty"`
}

func (u Update) Text() string {
	var block ContentBlock
	if json.Unmarshal(u.Content, &block) == nil {
		return block.Text
	}
	return ""
}

func (u Update) ToolContent() []ToolCallContent {
	var content []ToolCallContent
	_ = json.Unmarshal(u.Content, &content)
	return content
}

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type PermissionToolCall struct {
	ToolCallID string          `json:"toolCallId"`
	Title      *string         `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
}

type RequestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  PermissionToolCall `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

type RequestPermissionResult struct {
	Outcome PermissionOutcome `json:"outcome"`
}

const (
	OptionAllowOnce    = "allow_once"
	OptionAllowAlways  = "allow_always"
	OptionRejectOnce   = "reject_once"
	OptionRejectAlways = "reject_always"
)

func Selected(optionID string) RequestPermissionResult {
	return RequestPermissionResult{Outcome: PermissionOutcome{Outcome: "selected", OptionID: optionID}}
}

func Cancelled() RequestPermissionResult {
	return RequestPermissionResult{Outcome: PermissionOutcome{Outcome: "cancelled"}}
}

const (
	StopEndTurn         = "end_turn"
	StopMaxTokens       = "max_tokens"
	StopMaxTurnRequests = "max_turn_requests"
	StopRefusal         = "refusal"
	StopCancelled       = "cancelled"
)
