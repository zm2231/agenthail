package daemonapi

import "encoding/json"

const (
	PersonActionAPI      = "action"
	PersonActionSettings = "settings"
)

// A PersonAction is the exact request a gated command sends the daemon: an
// action body for /api/v1/actions, or a settings body. Its scope is read from
// the body, never from what the caller says it touches.
type PersonAction struct {
	API  string          `json:"api"`
	Body json.RawMessage `json:"body"`
}

// ActionActorHeader carries who started an action when the daemon is not
// the first hop: the CLI through its local daemon, or a person on another
// host through that host's daemon.
const ActionActorHeader = "Agenthail-Actor"

// CLIActor is how history names an action taken from the agenthail CLI.
const CLIActor = "the CLI"

const MCPClaimPath = "/api/v1/mcp/claim"
