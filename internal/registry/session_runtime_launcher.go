package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (r *Registry) SaveSessionRuntime(sessionID string, runtime *surface.Runtime) error {
	if sessionID == "" || runtime == nil || runtime.Launcher == "" {
		return fmt.Errorf("session runtime and launcher are required")
	}
	location, err := json.Marshal(runtime.Location)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`INSERT INTO session_runtime(session_id,runtime_launcher,runtime_location,runtime_focusable,updated_at)
VALUES(?,?,?,?,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET runtime_launcher=excluded.runtime_launcher,runtime_location=excluded.runtime_location,runtime_focusable=excluded.runtime_focusable,updated_at=datetime('now')`, sessionID, string(runtime.Launcher), location, b2i(runtime.Focusable))
	return err
}

func (r *Registry) SessionRuntime(sessionID string) (*surface.Runtime, bool, error) {
	var launcher string
	var location []byte
	var focusable int
	err := r.db.QueryRow(`SELECT runtime_launcher,runtime_location,runtime_focusable FROM session_runtime WHERE session_id=?`, sessionID).Scan(&launcher, &location, &focusable)
	if err == sql.ErrNoRows || launcher == "" {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	runtime, err := runtimeFromColumns(launcher, location, focusable)
	if err != nil {
		return nil, false, err
	}
	return runtime, true, nil
}

func runtimeFromColumns(launcher string, location []byte, focusable int) (*surface.Runtime, error) {
	if launcher == "" {
		return nil, nil
	}
	runtime := &surface.Runtime{Launcher: launcher, Focusable: focusable != 0}
	if len(location) > 0 && string(location) != "null" && string(location) != "{}" {
		if err := json.Unmarshal(location, &runtime.Location); err != nil {
			return nil, err
		}
	}
	return runtime, nil
}
