package daemon

import (
	"context"
	"encoding/json"
	"time"
)

type mcpProjectView struct {
	ID   string `json:"id"`
	Key  string `json:"key,omitempty"`
	Name string `json:"name"`
	Root string `json:"root"`
}

type mcpWhoamiView struct {
	SessionID    string            `json:"sessionId"`
	Alias        string            `json:"alias,omitempty"`
	Surface      string            `json:"surface"`
	Cwd          string            `json:"cwd"`
	Host         map[string]string `json:"host"`
	Project      *mcpProjectView   `json:"project"`
	Coordinator  bool              `json:"coordinator"`
	Capabilities []string          `json:"capabilities"`
	Ceiling      json.RawMessage   `json:"ceiling"`
	Credential   map[string]string `json:"credential"`
}

func (d *Daemon) mcpWhoami(_ context.Context, caller *mcpCaller, _ struct{}) (any, error) {
	alias, _ := d.Registry.ReverseAlias(caller.session.ID)
	host := d.hostIdentity()
	return mcpWhoamiView{
		SessionID:    caller.session.ID,
		Alias:        alias,
		Surface:      string(caller.session.Surface),
		Cwd:          caller.session.Cwd,
		Host:         map[string]string{"id": host.ID, "name": host.Name},
		Project:      projectView(caller.project),
		Coordinator:  d.coordinates(caller),
		Capabilities: caller.credential.Capabilities,
		Ceiling:      caller.credential.Ceiling,
		Credential: map[string]string{
			"idleExpiresAt": caller.credential.IdleExpiresAt.UTC().Format(time.RFC3339),
			"maxExpiresAt":  caller.credential.MaxExpiresAt.UTC().Format(time.RFC3339),
		},
	}, nil
}

func projectView(project *CatalogProject) *mcpProjectView {
	if project == nil {
		return nil
	}
	return &mcpProjectView{ID: project.ID, Key: project.Key, Name: project.DisplayName, Root: project.RootPath}
}

func (d *Daemon) coordinates(caller *mcpCaller) bool {
	if caller.project == nil {
		return false
	}
	spec, err := d.Registry.Coordinator(caller.project.ID)
	return err == nil && spec.SessionID == caller.session.ID
}
