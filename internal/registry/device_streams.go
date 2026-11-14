package registry

import (
	"strings"
	"time"
)

const (
	deviceStreamsKept = 500

	StreamEndedClientClosed  = "client_closed"
	StreamEndedWriteDeadline = "write_deadline"
	StreamEndedWriteFailed   = "write_failed"
	StreamEndedDaemonStopped = "daemon_stopping"
	StreamEndedUnauthorized  = "unauthorized"
	StreamEndedServer        = "ended"
)

type DeviceStream struct {
	Route       string    `json:"route"`
	OpenedAt    time.Time `json:"openedAt"`
	ClosedAt    time.Time `json:"closedAt"`
	LastFlushAt time.Time `json:"lastFlushAt,omitzero"`
	Ended       string    `json:"ended"`
	Bytes       int64     `json:"bytes"`
	TailnetPath string    `json:"tailnetPath"`
}

func (r *Registry) RecordDeviceStream(deviceID string, stream DeviceStream) error {
	deviceID = strings.TrimSpace(deviceID)
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO device_streams(device_id,route,opened_at_ms,closed_at_ms,last_flush_at_ms,ended,bytes,tailnet_path)
		SELECT id,?,?,?,?,?,?,? FROM paired_devices WHERE id=? AND revoked_at=''`,
		stream.Route, stream.OpenedAt.UnixMilli(), stream.ClosedAt.UnixMilli(), millisOrZero(stream.LastFlushAt), stream.Ended, stream.Bytes, stream.TailnetPath, deviceID)
	if err != nil {
		return err
	}
	if inserted, _ := result.RowsAffected(); inserted == 0 {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM device_streams WHERE device_id=? AND id NOT IN
		(SELECT id FROM device_streams WHERE device_id=? ORDER BY closed_at_ms DESC, id DESC LIMIT ?)`, deviceID, deviceID, deviceStreamsKept); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) DeviceStreams(deviceID, route string, from, to time.Time) ([]DeviceStream, error) {
	rows, err := r.read.Query(`SELECT route,opened_at_ms,closed_at_ms,last_flush_at_ms,ended,bytes,tailnet_path FROM device_streams
		WHERE device_id=? AND route=? AND opened_at_ms<=? AND closed_at_ms>=? ORDER BY closed_at_ms DESC, id DESC`,
		deviceID, route, to.UnixMilli(), from.UnixMilli())
	if err != nil {
		return nil, err
	}
	return appendRows([]DeviceStream{}, rows, func(scan rowScanner) (DeviceStream, error) {
		var stream DeviceStream
		var opened, closed, flushed int64
		if err := scan.Scan(&stream.Route, &opened, &closed, &flushed, &stream.Ended, &stream.Bytes, &stream.TailnetPath); err != nil {
			return stream, err
		}
		stream.OpenedAt, stream.ClosedAt = time.UnixMilli(opened).UTC(), time.UnixMilli(closed).UTC()
		if flushed > 0 {
			stream.LastFlushAt = time.UnixMilli(flushed).UTC()
		}
		return stream, nil
	})
}

func millisOrZero(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixMilli()
}
