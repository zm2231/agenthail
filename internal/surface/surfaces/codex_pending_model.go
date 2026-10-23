package surfaces

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/zm2231/agenthail/internal/atomicfile"
	"github.com/zm2231/agenthail/internal/surface"
)

// codexPendingModel is a model chosen for a thread Codex already has loaded.
// Codex rejoins a loaded thread on thread/resume and drops its overrides, so
// the model reaches the thread with the next turn Agenthail starts. From is
// the thread's model when it was chosen: a thread that has since moved to a
// third model was changed by someone else, and the choice no longer applies.
type codexPendingModel struct {
	Model string    `json:"model"`
	From  string    `json:"from"`
	SetAt time.Time `json:"setAt"`
}

var codexThreadIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func codexPendingModelDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "codex-pending-model")
}

func codexPendingModelPath(threadID string) string {
	if !codexThreadIDPattern.MatchString(threadID) {
		return ""
	}
	return filepath.Join(codexPendingModelDir(), threadID+".json")
}

func loadCodexPendingModel(threadID string) (codexPendingModel, bool) {
	path := codexPendingModelPath(threadID)
	if path == "" {
		return codexPendingModel{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return codexPendingModel{}, false
	}
	var pending codexPendingModel
	if json.Unmarshal(data, &pending) != nil || pending.Model == "" {
		return codexPendingModel{}, false
	}
	return pending, true
}

func saveCodexPendingModel(threadID string, pending codexPendingModel) error {
	path := codexPendingModelPath(threadID)
	if path == "" {
		return fmt.Errorf("codex thread id %q cannot name a pending model file", threadID)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0600)
}

func clearCodexPendingModel(threadID string) error {
	path := codexPendingModelPath(threadID)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func applyCodexPendingModel(session *surface.Session) {
	if pending, ok := loadCodexPendingModel(session.ID); ok {
		session.PendingModel = pending.Model
	} else {
		session.PendingModel = ""
	}
}

func codexPendingModelForTurn(threadID, current string) (model, notice string) {
	pending, ok := loadCodexPendingModel(threadID)
	if !ok {
		return "", ""
	}
	switch {
	case current == pending.Model:
		return "", ""
	case current != pending.From:
		return "", fmt.Sprintf("Dropped the pending model %s: the thread moved from %s to %s after it was chosen, so this turn runs on %s.", pending.Model, pending.From, current, current)
	}
	return pending.Model, ""
}

func (c *Codex) PendingModel(sessionID string) string {
	if pending, ok := loadCodexPendingModel(sessionID); ok {
		return pending.Model
	}
	return ""
}
