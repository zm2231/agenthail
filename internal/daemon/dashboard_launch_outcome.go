package daemon

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) writeAcceptedLaunch(w http.ResponseWriter, launcher string, location *surface.Location, detail, notice string) {
	d.log.Printf("accepted %s launch: %s", launcher, detail)
	warning := fmt.Sprintf("Submitted to %s.", launcher)
	if notice != "" {
		warning += " " + notice
	}
	body := map[string]any{
		"ok":        true,
		"status":    "submitted",
		"accepted":  true,
		"retryable": false,
		"launcher":  launcher,
		"warning":   warning,
	}
	if location != nil {
		body["location"] = location
	}
	writeDashboardJSON(w, http.StatusAccepted, body)
}

func untracked(alias string) string {
	return strings.TrimSpace("It appears in the sidebar once Agenthail finds it. " + unkeptName(alias))
}

func unkeptName(alias string) string {
	if alias == "" {
		return ""
	}
	return fmt.Sprintf("Its name @%s wasn't kept; name it once it appears in the sidebar.", alias)
}
