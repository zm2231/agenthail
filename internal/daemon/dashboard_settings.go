package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/deliverypolicy"
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
	config, err := LoadDashboardConfig()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if r.Method == http.MethodGet {
		writeDashboardJSON(w, http.StatusOK, map[string]any{"dashboard": config, "claudeAccounts": config.ClaudeAccountPolicy(), "claudeRoots": d.claudeRootNames(), "remoteAccess": RemoteAccessStatusForConfig(config), "notifications": GetNotificationStatus(), "daemon": map[string]any{"pid": os.Getpid(), "running": true}})
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
		Action           string `json:"action"`
		CodexRecentHours int    `json:"codexRecentHours"`
		BusyDelivery     string `json:"busyDelivery"`
		PersonName       string `json:"personName"`
		ResumeAfterLimit bool              `json:"resumeAfterLimit"`
		ClaudeAccounts   claudepick.Policy `json:"claudeAccounts"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "invalid settings request")
		return
	}
	switch request.Action {
	case "remote-enable":
		config, _, err = EnableRemoteAccess(config)
	case "remote-disable":
		config, err = DisableRemoteAccess(config)
	case "dashboard-config":
		config.CodexRecentHours = request.CodexRecentHours
		if request.BusyDelivery != "" {
			config.BusyDelivery = deliverypolicy.Mode(request.BusyDelivery)
		}
		if err = SaveDashboardConfig(config); err == nil {
			d.publishCatalogPresence()
		}
	case "resume-after-limit":
		config.ResumeAfterLimit = &request.ResumeAfterLimit
		err = SaveDashboardConfig(config)
	case "claude-accounts":
		if err = d.knownClaudeRoot(request.ClaudeAccounts.Preferred); err == nil {
			if err = config.SetClaudeAccountPolicy(request.ClaudeAccounts); err == nil {
				err = SaveDashboardConfig(config)
			}
		}
	case "person-name":
		err = d.Registry.SetPersonName(request.PersonName)
	case "notifications-enable":
		_, err = EnableNotifications()
	case "notifications-disable":
		err = DisableNotifications()
	case "notifications-settings":
		err = OpenNotificationSettings()
	case "notifications-test":
		if !GetNotificationStatus().Enabled {
			err = fmt.Errorf("desktop notifications are not enabled")
		} else {
			err = Notify("Agenthail", "Notifications are working", "", "")
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
	image, err := RemoteAccessQR(link.URL)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(image)
}
