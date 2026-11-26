package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/notify"
)

func (d *Daemon) person() (dashboardPerson, error) {
	setting, err := d.Registry.PersonNameSetting()
	if err != nil {
		return dashboardPerson{}, err
	}
	name, err := d.Registry.PersonName()
	return dashboardPerson{Name: name, Set: setting != ""}, err
}

func (d *Daemon) dashboardSettingsHandler(w http.ResponseWriter, r *http.Request) {
	config, err := dashconfig.Load()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if r.Method == http.MethodGet {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"dashboard": config, "claudeDefaultRoot": config.ClaudeDefaultRoot(), "claudeRoots": d.claudeRootNames(), "remoteAccess": RemoteAccessStatusForConfig(config), "notifications": notify.CurrentStatus(), "agents": d.agentStatuses(config), "daemon": map[string]any{"pid": os.Getpid(), "running": true}})
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	mutation := &mutationResponseWriter{ResponseWriter: w}
	w = mutation
	defer func() {
		if mutation.succeeded() {
			d.publishEvent("settings.updated", "", map[string]string{"source": "dashboard"})
		}
	}()
	var request struct {
		Action            string  `json:"action"`
		CodexRecentHours  *int    `json:"codexRecentHours"`
		BusyDelivery      *string `json:"busyDelivery"`
		PersonName        string  `json:"personName"`
		ResumeAfterLimit  *bool   `json:"resumeAfterLimit"`
		MoveAfterLimit    *bool   `json:"moveAfterLimit"`
		ClaudeDefaultRoot *string `json:"claudeDefaultRoot"`
		Agent             string  `json:"agent"`
		Mode              string  `json:"mode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid settings request")
		return
	}
	switch request.Action {
	case "remote-enable":
		if _, _, err = EnableRemoteAccess(config); err == nil {
			err = d.syncExtensionMounts()
		}
	case "remote-disable":
		if _, err = DisableRemoteAccess(config); err == nil {
			err = d.syncExtensionMounts()
		}
	case "dashboard-config":
		if request.CodexRecentHours == nil && request.BusyDelivery == nil {
			err = fmt.Errorf("dashboard-config names no setting: send codexRecentHours or busyDelivery")
			break
		}
		if _, err = dashconfig.Update(func(config *dashconfig.Config) error {
			if request.CodexRecentHours != nil {
				config.CodexRecentHours = *request.CodexRecentHours
			}
			if request.BusyDelivery != nil {
				config.BusyDelivery = deliverypolicy.Mode(*request.BusyDelivery)
			}
			return nil
		}); err == nil {
			d.publishCatalogPresence()
		}
	case "resume-after-limit":
		if request.ResumeAfterLimit == nil {
			err = fmt.Errorf("resume-after-limit needs resumeAfterLimit")
			break
		}
		_, err = dashconfig.Update(func(config *dashconfig.Config) error {
			config.ResumeAfterLimit = request.ResumeAfterLimit
			return nil
		})
	case "move-after-limit":
		if request.MoveAfterLimit == nil {
			err = fmt.Errorf("move-after-limit needs moveAfterLimit")
			break
		}
		_, err = dashconfig.Update(func(config *dashconfig.Config) error {
			config.MoveAfterLimit = request.MoveAfterLimit
			return nil
		})
	case "agent-mode":
		err = d.setAgentMode(request.Agent, request.Mode)
	case "claude-default-root":
		if request.ClaudeDefaultRoot == nil {
			err = fmt.Errorf("claude-default-root needs claudeDefaultRoot, empty for the primary root")
			break
		}
		if err = d.knownClaudeRoot(*request.ClaudeDefaultRoot); err == nil {
			_, err = dashconfig.Update(func(config *dashconfig.Config) error {
				config.SetClaudeDefaultRoot(*request.ClaudeDefaultRoot)
				return nil
			})
		}
	case "person-name":
		err = d.Registry.SetPersonName(request.PersonName)
	case "notifications-enable":
		_, err = notify.Enable()
	case "notifications-disable":
		err = notify.Disable()
	case "notifications-settings":
		err = notify.OpenSettings()
	case "notifications-test":
		if !notify.CurrentStatus().Enabled {
			err = fmt.Errorf("desktop notifications are not enabled")
		} else {
			err = notify.Notify("Agenthail", "Notifications are working", "", "")
		}
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "unsupported settings action")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (d *Daemon) dashboardRemoteQRHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	link, err := d.createLoginLink(true)
	if err == errRemoteAccessOff {
		writeAPIError(w, http.StatusConflict, "conflict", "remote access is not enabled")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	image, err := dashconfig.RemoteAccessQR(link.URL)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(image)
}
