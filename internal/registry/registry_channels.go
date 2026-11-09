package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

func (r *Registry) CreateChannel(name string) (string, error) {
	id := fmt.Sprintf("ch_%d", time.Now().UnixNano())
	_, err := r.db.Exec(`INSERT INTO channels (id,name) VALUES (?,?)`, id, name)
	return id, err
}

func (r *Registry) AddToChannel(channelName, sessionID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := channelsSettledTx(tx, sessionID); err != nil {
		return err
	}
	var home string
	if err := tx.QueryRow(`SELECT home_host_id FROM remote_channels WHERE session_id=? AND channel_name=?`, sessionID, channelName).Scan(&home); err == nil {
		return fmt.Errorf("session %s is already in #%s on host %s; leave it there first", sessionID, channelName, home)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO channel_members (channel_id,session_id) SELECT id,? FROM channels WHERE name = ?`, sessionID, channelName)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM channels WHERE name=?`, channelName).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return fmt.Errorf("channel %q does not exist", channelName)
		}
	}
	return tx.Commit()
}

type ChannelRow struct {
	Name        string
	MemberCount int
	Members     []string
}

func (r *Registry) ListChannels() ([]ChannelRow, error) {
	rows, err := r.read.Query(`
		SELECT c.name, COALESCE(count(cm.session_id),0) as n
		FROM channels c
		LEFT JOIN channel_members cm ON cm.channel_id = c.id
		GROUP BY c.id
		ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelRow
	for rows.Next() {
		var ch ChannelRow
		if err := rows.Scan(&ch.Name, &ch.MemberCount); err == nil {
			out = append(out, ch)
		}
	}
	for i := range out {
		members, _ := r.ChannelMembers(out[i].Name)
		out[i].Members = members
	}
	return out, nil
}

func (r *Registry) ChannelMembers(channelName string) ([]string, error) {
	rows, err := r.read.Query(`
		SELECT cm.session_id FROM channel_members cm
		JOIN channels c ON c.id = cm.channel_id
		WHERE c.name = ?`, channelName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err == nil {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (r *Registry) RemoveFromChannel(channelName, sessionID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := channelsSettledTx(tx, sessionID); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM channel_members WHERE channel_id = (SELECT id FROM channels WHERE name = ?) AND session_id = ?`, channelName, sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("session %q is not a member of channel %q", sessionID, channelName)
	}
	return tx.Commit()
}

func (r *Registry) ChannelExists(channelName string) (bool, error) {
	var exists bool
	err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM channels WHERE name=?)`, channelName).Scan(&exists)
	return exists, err
}

func (r *Registry) RenameChannel(channelName, newName string) error {
	if exists, err := r.ChannelExists(newName); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("channel #%s already exists", newName)
	}
	res, err := r.db.Exec(`UPDATE channels SET name=? WHERE name=?`, newName, channelName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("channel %q does not exist", channelName)
	}
	return nil
}

func (r *Registry) DeleteChannel(channelName string) error {
	res, err := r.db.Exec(`DELETE FROM channels WHERE name = ?`, channelName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("channel %q does not exist", channelName)
	}
	return nil
}

func channelsSettledTx(tx *sql.Tx, sessionID string) error {
	if moving, err := movingOutTx(tx, sessionID); err != nil {
		return err
	} else if moving {
		return fmt.Errorf("session %s is moving to another host; change its channels once the move has finished", sessionID)
	}
	return nil
}
