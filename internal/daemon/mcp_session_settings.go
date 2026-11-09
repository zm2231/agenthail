package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

type mcpSessionSettingsInput struct {
	mcpWrite
	Target  string `json:"target" jsonschema:"A session id or @alias you spawned or, as coordinator, lead; any you can read, to read."`
	Setting string `json:"setting,omitempty" jsonschema:"model, permissionMode (Claude), effort (Claude) or compact. Leave it out to read the settings."`
	Value   string `json:"value,omitempty" jsonschema:"The new model, permission mode or effort; compact takes none."`
}

var mcpSettingActions = map[string]string{"model": "model", "permissionMode": "permission-mode", "effort": "effort", "compact": "compact"}

func (d *Daemon) mcpSessionSettings(ctx context.Context, caller *mcpCaller, input mcpSessionSettingsInput) (any, error) {
	if strings.TrimSpace(input.Setting) == "" {
		if strings.TrimSpace(input.Value) != "" {
			return nil, refuse("invalid", "value needs a setting.")
		}
		session, err := d.mcpReadableSession(caller, input.Target)
		if err != nil {
			return nil, err
		}
		return d.sessionSettings(ctx, session)
	}
	action, ok := mcpSettingActions[strings.TrimSpace(input.Setting)]
	if !ok {
		return nil, refuse("invalid", "setting is model, permissionMode, effort or compact.")
	}
	value := strings.TrimSpace(input.Value)
	if action != "compact" && value == "" {
		return nil, refuse("invalid", "value is required for "+input.Setting+".")
	}
	target, err := d.mcpControlTarget(ctx, caller, input.Target, false)
	if err != nil {
		return nil, err
	}
	if action == "permission-mode" {
		if err := d.mcpPermissionWithinCeiling(caller, target, value); err != nil {
			return nil, err
		}
	}
	request := dashboardActionRequest{Action: action, SessionID: target.ID}
	switch action {
	case "model":
		request.Model = value
	case "permission-mode":
		request.PermissionMode = value
	case "effort":
		request.Message = value
	}
	return d.mcpAction(ctx, caller, "agenthail_session_settings", input.mcpWrite, request)
}

func (d *Daemon) mcpPermissionWithinCeiling(caller *mcpCaller, target *surface.Session, mode string) error {
	if target.Surface != surface.KindClaude {
		return refuse("unsupported_surface", "Only a running Claude session's permission mode can change; that session is "+string(target.Surface)+".")
	}
	rank, ok := claudePermissionRank[mode]
	if !ok {
		return refuse("invalid", fmt.Sprintf("Unknown Claude permission mode %q.", mode))
	}
	if caller.has("control.all") {
		return nil
	}
	raw, err := d.Registry.SessionCeiling(target.ID)
	if err != nil {
		return err
	}
	ceiling, err := parseCeiling(raw)
	if err != nil {
		return err
	}
	if ceiling.Permission != "" && rankOf(rank) > rankOf(ceiling.Permission) {
		return (&mcpRefusal{Code: "exceeds_ceiling", Message: fmt.Sprintf("%s is above that session's ceiling of %s.", mode, ceiling.Permission), Detail: map[string]any{"axis": "permission", "limit": ceiling.Permission}}).about(target.ID)
	}
	return nil
}
