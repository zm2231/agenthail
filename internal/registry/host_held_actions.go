package registry

import (
	"database/sql"
	"errors"
	"time"
)

type HostHeldAction struct {
	ID      int64
	HostID  string
	Key     string
	Label   string
	Request []byte
	Status  string
}

const (
	HostActionPending  = "pending"
	HostActionInflight = "inflight"
	HostActionDone     = "done"
	HostActionDead     = "dead"
)

// sent marks an action that may have reached the host; a key already held returns its row.
func (r *Registry) HoldHostAction(hostID, key, label string, request []byte, sent bool) (int64, error) {
	if hostID == "" || key == "" || len(request) == 0 {
		return 0, errors.New("a held host action needs its host, idempotency key and request")
	}
	now := r.now().UnixMilli()
	status, inflightAt := HostActionPending, int64(0)
	if sent {
		status, inflightAt = HostActionInflight, now
	}
	var id int64
	err := r.db.QueryRow(`INSERT INTO host_held_actions(host_id,delivery_key,label,request,status,inflight_at_ms,created_at_ms,updated_at_ms)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(delivery_key) DO UPDATE SET delivery_key=excluded.delivery_key RETURNING id`,
		hostID, key, label, request, status, inflightAt, now, now).Scan(&id)
	if err != nil {
		return 0, err
	}
	r.signalChange(ChangeHostDeliveries)
	return id, nil
}

func (r *Registry) HostHeldActions(hostID string) ([]HostHeldAction, error) {
	rows, err := r.read.Query(`SELECT id,host_id,delivery_key,label,request,status FROM host_held_actions WHERE host_id=? AND status IN ('pending','inflight') ORDER BY id`, hostID)
	if err != nil {
		return nil, err
	}
	return appendRows([]HostHeldAction{}, rows, func(row rowScanner) (HostHeldAction, error) {
		var item HostHeldAction
		err := row.Scan(&item.ID, &item.HostID, &item.Key, &item.Label, &item.Request, &item.Status)
		return item, err
	})
}

// A row another drain already settled is not claimed and must not be sent.
func (r *Registry) ClaimHostHeldAction(id int64) (time.Time, bool, error) {
	now := r.now().UnixMilli()
	var inflightAt int64
	err := r.db.QueryRow(`UPDATE host_held_actions SET inflight_at_ms=CASE WHEN status='pending' THEN ? ELSE inflight_at_ms END,status='inflight',attempts=attempts+1,updated_at_ms=?
		WHERE id=? AND status IN ('pending','inflight') RETURNING inflight_at_ms`, now, now, id).Scan(&inflightAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.UnixMilli(inflightAt), true, nil
}

// Only for a claimed action that provably never left this daemon.
func (r *Registry) ReleaseHostHeldAction(id int64) error {
	_, err := r.db.Exec(`UPDATE host_held_actions SET status='pending',inflight_at_ms=0,updated_at_ms=? WHERE id=? AND status='inflight'`, r.now().UnixMilli(), id)
	return err
}

func (r *Registry) SettleHostHeldAction(id int64, status, outcome string) error {
	if status != HostActionDone && status != HostActionDead {
		return errors.New("a held host action settles as done or dead")
	}
	result, err := r.db.Exec(`UPDATE host_held_actions SET status=?,outcome=?,updated_at_ms=? WHERE id=? AND status IN ('pending','inflight')`, status, outcome, r.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 1 {
		r.signalChange(ChangeHostDeliveries)
	}
	return nil
}
