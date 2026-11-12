package daemon

import (
	"slices"

	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/surface"
)

// Tools that act for an agent's own turn, task or ledger need a session
// behind the caller, so extensions get only these. A write takes a
// clientRequestId, which the extension host adds when the caller leaves it out.
var extensionTools = map[string]bool{
	"agenthail_sessions":     false,
	"agenthail_read":         false,
	"agenthail_history":      false,
	"agenthail_projects":     false,
	"agenthail_requests":     false,
	"agenthail_answer":       true,
	"agenthail_send":         true,
	"agenthail_channel_send": true,
}

func extensionWriteTools() []string {
	var writes []string
	for name, write := range extensionTools {
		if write {
			writes = append(writes, name)
		}
	}
	slices.Sort(writes)
	return writes
}

func init() {
	for name := range extensionTools {
		if _, ok := agenttools.Lookup(name); !ok {
			panic("extension tool " + name + " is not an Agenthail tool")
		}
	}
}

func (d *Daemon) extensionCaller(token string) (*mcpCaller, bool) {
	id, capabilities, ok := d.extensions.extensionByToken(token)
	if !ok {
		return nil, false
	}
	label := id
	if loaded, ok := d.extensions.find(id); ok && loaded.ext.Manifest.Label != "" {
		label = loaded.ext.Manifest.Label
	}
	return &mcpCaller{session: surface.ExtensionSender(id, label), capabilities: capabilities, extension: id}, true
}
