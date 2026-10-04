package registry

import (
	"encoding/json"
	"fmt"
	"github.com/zm2231/agenthail/internal/surface"
)

type PendingLaunch struct {
	ID        int64
	Launcher  string
	Agent     surface.SurfaceKind
	Cwd, Name string
	Location  surface.Location
}

func (r *Registry) RecordPendingLaunch(launcher string, agent surface.SurfaceKind, cwd, name string, location surface.Location) (int64, error) {
	b, err := json.Marshal(location)
	if err != nil {
		return 0, err
	}
	result, err := r.db.Exec(`INSERT INTO launcher_pending(launcher,agent,cwd,name,location) VALUES(?,?,?,?,?)`, string(launcher), string(agent), cwd, name, b)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *Registry) PendingLaunches() ([]PendingLaunch, error) {
	rows, err := r.db.Query(`SELECT id,launcher,agent,cwd,name,location FROM launcher_pending ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PendingLaunch{}
	for rows.Next() {
		var item PendingLaunch
		var launcher, agent string
		var location []byte
		if err := rows.Scan(&item.ID, &launcher, &agent, &item.Cwd, &item.Name, &location); err != nil {
			return nil, err
		}
		item.Launcher = launcher
		item.Agent = surface.SurfaceKind(agent)
		if err := json.Unmarshal(location, &item.Location); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Registry) DeletePendingLaunch(id int64) error {
	if id <= 0 {
		return fmt.Errorf("pending launch id is required")
	}
	_, err := r.db.Exec(`DELETE FROM launcher_pending WHERE id=?`, id)
	return err
}
