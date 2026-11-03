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
	ReceivedAt   string `json:"receivedAt,omitempty"`
	Alerts       string `json:"alerts,omitempty"`
	Unarrived    int    `json:"unarrived"`
}

// Concurrent sends land in any order, so each field keeps its latest time.
func (r *Registry) RecordPushOutcome(deviceID, eventType string, at time.Time, failure error) error {
	stamp := formatTimestamp(at)
	message, failedAt, succeededAt := "", "", stamp
	if failure != nil {
		message, failedAt, succeededAt = failure.Error(), stamp, ""
		if len(message) > 500 {
			message = message[:500]
		}
	}
	_, err := r.db.Exec(`INSERT INTO push_outcomes (device_id,event_type,attempted_at,succeeded_at,error,failed_at)
		SELECT id,?,?,?,?,? FROM paired_devices WHERE id=?
		ON CONFLICT(device_id) DO UPDATE SET
			event_type=CASE WHEN `+laterStamp("attempted_at")+` THEN excluded.event_type ELSE push_outcomes.event_type END,
			attempted_at=CASE WHEN `+laterStamp("attempted_at")+` THEN excluded.attempted_at ELSE push_outcomes.attempted_at END,
			succeeded_at=CASE WHEN excluded.succeeded_at!='' AND `+laterStamp("succeeded_at")+` THEN excluded.succeeded_at ELSE push_outcomes.succeeded_at END,
			error=CASE WHEN excluded.failed_at!='' AND `+laterStamp("failed_at")+` THEN excluded.error ELSE push_outcomes.error END,
			failed_at=CASE WHEN excluded.failed_at!='' AND `+laterStamp("failed_at")+` THEN excluded.failed_at ELSE push_outcomes.failed_at END`,
		eventType, stamp, succeededAt, message, failedAt, strings.TrimSpace(deviceID))
	return err
}

func laterStamp(column string) string {
	return "(push_outcomes." + column + "='' OR julianday(excluded." + column + ")>=julianday(push_outcomes." + column + "))"
}

// Unarrived counts pushes the relay forwarded with a receipt requested, sent
// before sentBefore and after the device's last receipt, that never arrived.
func (r *Registry) PushStatuses(sentBefore time.Time) ([]PushStatus, error) {
	rows, err := r.read.Query(`SELECT p.device_id,d.name,p.enabled,p.updated_at,
			COALESCE(o.event_type,''),COALESCE(o.attempted_at,''),COALESCE(o.succeeded_at,''),COALESCE(o.error,''),COALESCE(o.failed_at,''),
			COALESCE(o.received_at,''),COALESCE(o.alerts,''),
			(SELECT COUNT(*) FROM push_sends s WHERE s.device_id=p.device_id AND s.received_at=''
				AND julianday(s.sent_at)<julianday(?)
				AND (COALESCE(o.received_at,'')='' OR julianday(s.sent_at)>julianday(o.received_at)))
		FROM device_push_targets p JOIN paired_devices d ON d.id=p.device_id
		LEFT JOIN push_outcomes o ON o.device_id=p.device_id
		WHERE d.revoked_at='' ORDER BY p.updated_at DESC`, formatTimestamp(sentBefore))
	if err != nil {
		return nil, err
	}
	return appendRows([]PushStatus{}, rows, func(row rowScanner) (PushStatus, error) {
		var status PushStatus
		var enabled int
		err := row.Scan(&status.DeviceID, &status.Name, &enabled, &status.RegisteredAt, &status.EventType, &status.AttemptedAt, &status.SucceededAt, &status.Error, &status.FailedAt,
			&status.ReceivedAt, &status.Alerts, &status.Unarrived)
		status.Enabled = enabled != 0
		return status, err
	})
}
