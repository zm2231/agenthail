package daemon

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type sessionSettingsRoot struct {
	Name       string `json:"name"`
	Root       string `json:"root"`
	Source     string `json:"source"`
	Missing    bool   `json:"missing,omitempty"`
	SignedInAs string `json:"signedInAs,omitempty"`
}

type sessionSettingsView struct {
	SessionID         string                     `json:"sessionId"`
	Surface           surface.SurfaceKind        `json:"surface"`
	RuntimeKind       surface.RuntimeKind        `json:"runtimeKind,omitempty"`
	Transport         string                     `json:"transport,omitempty"`
	Cwd               string                     `json:"cwd,omitempty"`
	ConfigRoot        *sessionSettingsRoot       `json:"configRoot"`
	Model             *surface.ModelSetting      `json:"model"`
	Effort            *surface.SettingValue      `json:"effort"`
	Permission        *surface.PermissionSetting `json:"permission"`
	CollaborationMode *surface.SettingValue      `json:"collaborationMode,omitempty"`
	Ceiling           *sessionCeiling            `json:"ceiling"`
	Unavailable       map[string]string          `json:"unavailable,omitempty"`
}

func (d *Daemon) sessionSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for session settings.")
		return
	}
	session, err := d.Registry.Session(r.URL.Query().Get("id"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	view, err := d.sessionSettings(r.Context(), session)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "bad_gateway", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, view)
}

// sessionSettings reads what a session runs with now. It never wakes, resumes
// or writes to the session; a field it cannot read is null with the reason.
func (d *Daemon) sessionSettings(ctx context.Context, session *surface.Session) (sessionSettingsView, error) {
	view := sessionSettingsView{SessionID: session.ID, Surface: session.Surface, RuntimeKind: session.RuntimeKind, Transport: session.Transport, Cwd: session.Cwd}
	raw, err := d.Registry.SessionCeiling(session.ID)
	if err != nil {
		return view, err
	}
	if ceiling, err := parseCeiling(raw); err != nil {
		return view, err
	} else if ceiling.Permission != "" || len(ceiling.Roots) > 0 {
		view.Ceiling = &ceiling
	}
	var runtime surface.RuntimeSettings
	adapter, err := d.adapters().For(session)
	if reader, ok := adapter.(surface.SessionSettingsReader); err == nil && ok {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if runtime, err = reader.SessionSettings(ctx, session); err != nil {
			return view, err
		}
	} else {
		for _, field := range []string{"model", "effort", "permission"} {
			runtime.Missing(field, "this surface does not report its settings")
		}
	}
	view.Model, view.Effort, view.Permission, view.CollaborationMode, view.Unavailable = runtime.Model, runtime.Effort, runtime.Permission, runtime.CollaborationMode, runtime.Unavailable
	if view.Model == nil && session.ConfiguredModel != "" {
		view.Model = &surface.ModelSetting{Value: session.ConfiguredModel, Source: surface.SettingFromStarted}
		delete(view.Unavailable, "model")
	}
	if session.Surface == surface.KindClaude {
		view.ConfigRoot = d.sessionSettingsRoot(session)
		if view.ConfigRoot == nil {
			view.unavailable("configRoot", "the conversation's transcript is in no configured Claude root")
		}
	} else {
		view.unavailable("configRoot", "only Claude sessions run in a config root")
	}
	return view, nil
}

func (v *sessionSettingsView) unavailable(field, why string) {
	if v.Unavailable == nil {
		v.Unavailable = map[string]string{}
	}
	v.Unavailable[field] = why
}

func (d *Daemon) sessionSettingsRoot(session *surface.Session) *sessionSettingsRoot {
	if session.Transcript == "" {
		return nil
	}
	root, ok := surface.ClaudeConfigRootFor(d.currentClaudeConfigRoots(), filepath.Dir(filepath.Dir(filepath.Dir(session.Transcript))))
	if !ok {
		return nil
	}
	view := &sessionSettingsRoot{Name: root.Name, Root: root.Root, Source: root.Source, Missing: root.Missing}
	if root.SignedInAs != nil {
		view.SignedInAs = root.SignedInAs.Email
	}
	return view
}
