package daemon

import (
	"github.com/zm2231/agenthail/internal/listeners"
	"github.com/zm2231/agenthail/internal/surface"
)

type browserServer struct {
	Port    int    `json:"port"`
	Address string `json:"address"`
	PID     int    `json:"pid,omitempty"`
	Command string `json:"command,omitempty"`
	Session string `json:"session,omitempty"`
	Mine    bool   `json:"mine,omitempty"`
}

func (d *Daemon) browserServers(caller *mcpCaller) (any, error) {
	variables := make([]string, len(surface.CallerIdentity))
	for index, source := range surface.CallerIdentity {
		variables[index] = source.Variable
	}
	found, err := listeners.Scan(variables)
	if err != nil {
		return nil, refuse("unavailable", "Listing this host's servers failed: "+err.Error())
	}
	visible, err := d.mcpVisibleSessions(caller, mcpFilterAllVisible)
	if err != nil {
		return nil, err
	}
	readable := map[string]bool{caller.session.ID: true}
	for _, session := range visible {
		readable[session.ID] = true
	}
	servers := make([]browserServer, 0, len(found))
	for _, listener := range found {
		server := browserServer{Port: listener.Port, Address: listener.Address, PID: listener.PID, Command: listener.Command}
		if session := d.listenerSession(listener.Env); readable[session] {
			server.Session, server.Mine = session, session == caller.session.ID
		}
		servers = append(servers, server)
	}
	return map[string]any{"servers": servers}, nil
}

func (d *Daemon) listenerSession(env map[string]string) string {
	for _, source := range surface.CallerIdentity {
		if value := env[source.Variable]; value != "" {
			id, err := d.Registry.ExactTarget(surface.SurfaceKind(source.Surface), value)
			if err != nil {
				return ""
			}
			return id
		}
	}
	return ""
}
