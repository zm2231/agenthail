package registry

import (
	"database/sql"
	"strings"
	"time"
)

const connectionDropsKept = 50

// Via is the computer that failed to reach Host for the phone.
type ConnectionDrop struct {
	ID                string    `json:"id"`
	Host              string    `json:"host"`
	Via               string    `json:"via,omitempty"`
	Path              string    `json:"path"`
	StartedAt         time.Time `json:"startedAt"`
	RecoveredAt       time.Time `json:"recoveredAt"`
	RecoveredBy       string    `json:"recoveredBy,omitempty"`
	FirstError        string    `json:"firstError"`
	LastError         string    `json:"lastError"`
	ElapsedMs         *int64    `json:"elapsedMs,omitempty"`
	Attempts          int       `json:"attempts"`
	Shown             string    `json:"shown"`
	SinceForegroundMs *int64    `json:"sinceForegroundMs,omitempty"`
	WentBackground    bool      `json:"wentBackground"`
	Network           string    `json:"network,omitempty"`
	VPN               bool      `json:"vpn"`
	Expensive         bool      `json:"expensive"`
	Constrained       bool      `json:"constrained"`
}

type DeviceConnectionDrops struct {
	DeviceID string           `json:"deviceId"`
	Name     string           `json:"name"`
	Drops    []ConnectionDrop `json:"drops"`
}

func (r *Registry) RecordConnectionDrops(deviceID string, drops []ConnectionDrop) (int, error) {
	deviceID = strings.TrimSpace(deviceID)
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	recorded := 0
	for _, drop := range drops {
		result, err := tx.Exec(`INSERT OR IGNORE INTO device_connection_drops
			(device_id,drop_id,host,via,path,started_at,recovered_at,recovered_by,first_error,last_error,elapsed_ms,attempts,shown,since_foreground_ms,went_background,network,vpn,expensive,constrained)
			SELECT id,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,? FROM paired_devices WHERE id=? AND revoked_at=''`,
			drop.ID, drop.Host, drop.Via, drop.Path, formatTimestamp(drop.StartedAt), formatTimestamp(drop.RecoveredAt), drop.RecoveredBy,
			drop.FirstError, drop.LastError, nullable(drop.ElapsedMs), drop.Attempts, drop.Shown, nullable(drop.SinceForegroundMs),
			drop.WentBackground, drop.Network, drop.VPN, drop.Expensive, drop.Constrained, deviceID)
		if err != nil {
			return 0, err
		}
		if changed, _ := result.RowsAffected(); changed > 0 {
			recorded++
		}
	}
	if _, err := tx.Exec(`DELETE FROM device_connection_drops WHERE device_id=? AND drop_id NOT IN
		(SELECT drop_id FROM device_connection_drops WHERE device_id=? ORDER BY julianday(started_at) DESC, drop_id DESC LIMIT ?)`,
		deviceID, deviceID, connectionDropsKept); err != nil {
		return 0, err
	}
	return recorded, tx.Commit()
}

func (r *Registry) ConnectionDropHistories() ([]DeviceConnectionDrops, error) {
	rows, err := r.read.Query(`SELECT c.device_id,d.name,c.drop_id,c.host,c.via,c.path,c.started_at,c.recovered_at,c.recovered_by,
			c.first_error,c.last_error,c.elapsed_ms,c.attempts,c.shown,c.since_foreground_ms,c.went_background,c.network,c.vpn,c.expensive,c.constrained
		FROM device_connection_drops c JOIN paired_devices d ON d.id=c.device_id
		WHERE d.revoked_at='' ORDER BY d.name, c.device_id, julianday(c.started_at) DESC, c.drop_id DESC`)
	if err != nil {
		return nil, err
	}
	type row struct {
		device, name string
		drop         ConnectionDrop
	}
	scanned, err := appendRows([]row{}, rows, func(scan rowScanner) (row, error) {
		var out row
		var started, recovered string
		var elapsed, foreground sql.NullInt64
		err := scan.Scan(&out.device, &out.name, &out.drop.ID, &out.drop.Host, &out.drop.Via, &out.drop.Path, &started, &recovered, &out.drop.RecoveredBy,
			&out.drop.FirstError, &out.drop.LastError, &elapsed, &out.drop.Attempts, &out.drop.Shown, &foreground, &out.drop.WentBackground,
			&out.drop.Network, &out.drop.VPN, &out.drop.Expensive, &out.drop.Constrained)
		if err != nil {
			return out, err
		}
		out.drop.StartedAt, _ = parseTimestamp(started)
		out.drop.RecoveredAt, _ = parseTimestamp(recovered)
		if elapsed.Valid {
			out.drop.ElapsedMs = &elapsed.Int64
		}
		if foreground.Valid {
			out.drop.SinceForegroundMs = &foreground.Int64
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	histories := []DeviceConnectionDrops{}
	for _, item := range scanned {
		if len(histories) == 0 || histories[len(histories)-1].DeviceID != item.device {
			histories = append(histories, DeviceConnectionDrops{DeviceID: item.device, Name: item.name})
		}
		last := &histories[len(histories)-1]
		last.Drops = append(last.Drops, item.drop)
	}
	return histories, nil
}

func nullable(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
