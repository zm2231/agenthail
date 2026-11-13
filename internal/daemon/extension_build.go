package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/extension"
	"github.com/zm2231/agenthail/internal/surface"
)

const extensionBuildLine = "Build or change an Agenthail extension in this folder. Read AGENTS.md first."

// The builder is an ordinary session the person starts: the folder is its
// working directory and it keeps the surface's own permission mode, so it
// asks only what the person's other sessions ask.
func (d *Daemon) apiExtensionBuildHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Message string `json:"message"`
		Surface string `json:"surface"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid", "Send {\"message\": \"<what to build>\", \"surface\": \"claude\" or \"codex\"}.")
		return
	}
	message := strings.TrimSpace(request.Message)
	if message == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid", "Say what the extension should do.")
		return
	}
	kind := surface.SurfaceKind(strings.TrimSpace(request.Surface))
	if kind == "" {
		kind = surface.KindClaude
	}
	if kind != surface.KindClaude && kind != surface.KindCodex {
		writeAPIError(w, http.StatusBadRequest, "invalid", "The builder is claude or codex.")
		return
	}
	workspace, err := d.prepareExtensionWorkspace()
	if err != nil {
		writeActionResult(w, actionResult{}, err)
		return
	}
	body, err := json.Marshal(map[string]string{"action": "session-create", "surface": string(kind), "cwd": workspace, "message": extensionBuildLine + "\n\n" + message})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	key := strings.TrimSpace(r.Header.Get(actionIdempotencyHeader))
	if key == "" {
		key = "extension-build-" + uuid.NewString()
	}
	d.performAction(r.Context(), principalFromRequest(r), actionFormatAPIv1, body, key).write(w)
}

func (d *Daemon) prepareExtensionWorkspace() (string, error) {
	if d.extensions == nil {
		return "", errExtensionsOff
	}
	workspace := extension.Workspace(d.extensions.home)
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return "", err
	}
	if err := d.seedExtensionWorkspace(false); err != nil {
		return "", err
	}
	return workspace, nil
}

// The guide is seeded before the first turn can look at the folder, so its
// files are Agenthail's commit and not that turn's.
func (d *Daemon) seedExtensionWorkspace(existing bool) error {
	history, err := d.openExtensionHistory(existing)
	if err != nil || history == nil {
		return err
	}
	return history.Seed(d.extensions.home)
}
