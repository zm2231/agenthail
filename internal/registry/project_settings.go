package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ProjectSetting is one named setting of a project. A shared project's
// settings reach every paired host; of two records for one setting the later
// wins, the origin host breaking a tie.
type ProjectSetting struct {
	Project    string    `json:"project"`
	Name       string    `json:"name"`
	Value      string    `json:"value"`
	ChangedAt  time.Time `json:"changedAt"`
	OriginHost string    `json:"originHost"`
}

const (
	ProjectSettingMCP = "mcp"
	ProjectMCPOn      = "on"
	ProjectMCPOff     = "off"
)

var projectSettingValues = map[string][]string{
	ProjectSettingMCP: {ProjectMCPOn, ProjectMCPOff},
}

func validProjectSetting(name, value string) bool {
	return slices.Contains(projectSettingValues[name], value)
}

func (s ProjectSetting) newerThan(other ProjectSetting) bool {
	if !s.ChangedAt.Equal(other.ChangedAt) {
		return s.ChangedAt.After(other.ChangedAt)
	}
	return s.OriginHost > other.OriginHost
}

func (r *Registry) ProjectSettings() ([]ProjectSetting, error) {
	rows, err := r.read.Query(`SELECT project_id,name,value,changed_at_ms,origin_host FROM project_settings ORDER BY project_id,name`)
	if err != nil {
		return nil, err
	}
	return appendRows([]ProjectSetting{}, rows, scanProjectSetting)
}

// ProjectSettingValue is the setting's value, or its first allowed value
// when nobody has set it.
func (r *Registry) ProjectSettingValue(project, name string) (string, error) {
	var value string
	err := r.read.QueryRow(`SELECT value FROM project_settings WHERE project_id=? AND name=?`, project, name).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return projectSettingValues[name][0], nil
	}
	return value, err
}

func (r *Registry) SetProjectSetting(project, name, value string) (ProjectSetting, error) {
	if project == "" || !validProjectSetting(name, value) {
		return ProjectSetting{}, fmt.Errorf("%s can be %v", name, projectSettingValues[name])
	}
	identity, err := r.HostIdentity()
	if err != nil {
		return ProjectSetting{}, fmt.Errorf("read this host's identity: %w", err)
	}
	setting := ProjectSetting{Project: project, Name: name, Value: value, ChangedAt: r.now().Truncate(time.Millisecond), OriginHost: identity.ID}
	tx, err := r.db.Begin()
	if err != nil {
		return ProjectSetting{}, err
	}
	defer tx.Rollback()
	if current, found, err := projectSettingTx(tx, project, name); err != nil {
		return ProjectSetting{}, err
	} else if found && !setting.ChangedAt.After(current.ChangedAt) {
		setting.ChangedAt = current.ChangedAt.Add(time.Millisecond)
	}
	if err := putProjectSettingTx(tx, setting); err != nil {
		return ProjectSetting{}, err
	}
	return setting, tx.Commit()
}

func (r *Registry) AdoptProjectSettings(settings []ProjectSetting) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed := false
	for _, setting := range settings {
		if !SharedProjectID(setting.Project) || setting.OriginHost == "" || !validProjectSetting(setting.Name, setting.Value) {
			continue
		}
		if setting.Project, err = movedProjectID(tx, setting.Project); err != nil {
			return false, err
		}
		current, found, err := projectSettingTx(tx, setting.Project, setting.Name)
		if err != nil {
			return false, err
		}
		if found && !setting.newerThan(current) {
			continue
		}
		if err := putProjectSettingTx(tx, setting); err != nil {
			return false, err
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, tx.Commit()
}

func scanProjectSetting(row rowScanner) (ProjectSetting, error) {
	var setting ProjectSetting
	var at int64
	err := row.Scan(&setting.Project, &setting.Name, &setting.Value, &at, &setting.OriginHost)
	setting.ChangedAt = time.UnixMilli(at)
	return setting, err
}

func projectSettingTx(tx *sql.Tx, project, name string) (ProjectSetting, bool, error) {
	setting, err := scanProjectSetting(tx.QueryRow(`SELECT project_id,name,value,changed_at_ms,origin_host FROM project_settings WHERE project_id=? AND name=?`, project, name))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectSetting{}, false, nil
	}
	return setting, err == nil, err
}

func putProjectSettingTx(tx *sql.Tx, setting ProjectSetting) error {
	if _, err := tx.Exec(`INSERT INTO project_settings(project_id,name,value,changed_at_ms,origin_host) VALUES(?,?,?,?,?)
		ON CONFLICT(project_id,name) DO UPDATE SET value=excluded.value, changed_at_ms=excluded.changed_at_ms, origin_host=excluded.origin_host`,
		setting.Project, setting.Name, setting.Value, setting.ChangedAt.UnixMilli(), setting.OriginHost); err != nil {
		return err
	}
	if !SharedProjectID(setting.Project) {
		return nil
	}
	payload, err := json.Marshal(setting)
	if err != nil {
		return err
	}
	_, _, err = appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("project.setting:%s:%s:%d:%s", setting.Project, setting.Name, setting.ChangedAt.UnixMilli(), setting.OriginHost), Type: "project.setting", EntityID: setting.Project, Payload: payload})
	return err
}

// A moved project keeps its settings under its new key, except one the new
// key already has.
func moveProjectSettingsTx(tx *sql.Tx, move ProjectMove) error {
	rows, err := tx.Query(`SELECT project_id,name,value,changed_at_ms,origin_host FROM project_settings WHERE project_id=?`, move.From)
	if err != nil {
		return err
	}
	settings, err := appendRows([]ProjectSetting{}, rows, scanProjectSetting)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM project_settings WHERE project_id=?`, move.From); err != nil {
		return err
	}
	for _, setting := range settings {
		if _, taken, err := projectSettingTx(tx, move.To, setting.Name); err != nil || taken {
			if err != nil {
				return err
			}
			continue
		}
		at := move.ChangedAt
		if !at.After(setting.ChangedAt) {
			at = setting.ChangedAt.Add(time.Millisecond)
		}
		if err := putProjectSettingTx(tx, ProjectSetting{Project: move.To, Name: setting.Name, Value: setting.Value, ChangedAt: at, OriginHost: move.OriginHost}); err != nil {
			return err
		}
	}
	return nil
}
