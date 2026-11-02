package registry

import (
	"strings"
	"time"
)

type PushStatus struct {
	DeviceID     string `json:"deviceId"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	RegisteredAt string `json:"registeredAt"`
	EventType    string `json:"eventType,omitempty"`
	AttemptedAt  string `json:"attemptedAt,omitempty"`
	SucceededAt  string `json:"succeededAt,omitempty"`
	Error        string `json:"error,omitempty"`
	FailedAt     string `json:"failedAt,omitempty"`
}

// A success leaves the last error in place, so doctor shows both.
func (r *Registry) RecordPushOutcome(deviceID, eventType string, at time.Time, failure error) error {
	stamp := formatTimestamp(at)
	if failure == nil {
		_, err := r.db.Exec(`INSERT INTO push_outcomes (device_id,event_type,attempted_at,succeeded_at)
			SELECT id,?,?,? FROM paired_devices WHERE id=?
			ON CONFLICT(device_id) DO UPDATE SET event_type=excluded.event_type,attempted_at=excluded.attempted_at,succeeded_at=excluded.succeeded_at`,
			eventType, stamp, stamp, strings.TrimSpace(deviceID))
		return err
	}
	message := failure.Error()
	if len(message) > 500 {
		message = message[:500]
	}
	_, err := r.db.Exec(`INSERT INTO push_outcomes (device_id,event_type,attempted_at,error,failed_at)
		SELECT id,?,?,?,? FROM paired_devices WHERE id=?
		ON CONFLICT(device_id) DO UPDATE SET event_type=excluded.event_type,attempted_at=excluded.attempted_at,error=excluded.error,failed_at=excluded.failed_at`,
		eventType, stamp, message, stamp, strings.TrimSpace(deviceID))
	return err
}

func (r *Registry) PushStatuses() ([]PushStatus, error) {
	rows, err := r.read.Query(`SELECT p.device_id,d.name,p.enabled,p.updated_at,
			COALESCE(o.event_type,''),COALESCE(o.attempted_at,''),COALESCE(o.succeeded_at,''),COALESCE(o.error,''),COALESCE(o.failed_at,'')
		FROM device_push_targets p JOIN paired_devices d ON d.id=p.device_id
		LEFT JOIN push_outcomes o ON o.device_id=p.device_id
		WHERE d.revoked_at='' ORDER BY p.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	return appendRows([]PushStatus{}, rows, func(row rowScanner) (PushStatus, error) {
		var status PushStatus
		var enabled int
		err := row.Scan(&status.DeviceID, &status.Name, &enabled, &status.RegisteredAt, &status.EventType, &status.AttemptedAt, &status.SucceededAt, &status.Error, &status.FailedAt)
		status.Enabled = enabled != 0
		return status, err
	})
}
