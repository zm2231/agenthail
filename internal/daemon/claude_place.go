package daemon

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) placeClaudeSession(creator, previous string) claudepick.Pick {
	config, err := LoadDashboardConfig()
	if err != nil {
		d.log.Printf("read the default Claude root: %s", err)
	}
	origin := claudepick.Origin{CreatorTranscript: d.sessionTranscript(creator), PreviousTranscript: d.sessionTranscript(previous)}
	return claudepick.Place(d.claudeConfigRoots(), config.ClaudeDefaultRoot(), origin)
}

func (d *Daemon) sessionTranscript(id string) string {
	if id == "" {
		return ""
	}
	session, err := d.Registry.Session(id)
	if err != nil || session.Surface != surface.KindClaude {
		return ""
	}
	return session.Transcript
}

func (d *Daemon) resolveClaudeStart(kind surface.SurfaceKind, creator string, options *surface.SessionStartOptions) *claudepick.Pick {
	if kind != surface.KindClaude || options.ConfigRoot != "" {
		return nil
	}
	pick := d.placeClaudeSession(creator, "")
	options.ConfigRoot = pick.Root
	return &pick
}

func (d *Daemon) claudeRootNames() []string {
	names := []string{}
	for _, root := range d.claudeConfigRoots() {
		names = append(names, root.Name)
	}
	return names
}

func (d *Daemon) knownClaudeRoot(name string) error {
	if name == "" || slices.Contains(d.claudeRootNames(), name) {
		return nil
	}
	return fmt.Errorf("no configured Claude config root is named %q", name)
}

func (d *Daemon) claudeRootPickHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"pick": d.placeClaudeSession("", "")})
}
