package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// MovedChannel is a channel a moving session belongs to, named with the host
// that keeps it: the source names itself for its own channels.
type MovedChannel struct {
	Name string `json:"name"`
	Home string `json:"home"`
}

type MoveCarry struct {
	Relays   []MovedRelay   `json:"relays,omitempty"`
	Channels []MovedChannel `json:"channels,omitempty"`
}

func (r *Registry) MoveCarry(conversation, self string) (MoveCarry, error) {
	return moveCarry(r.read, conversation, self)
}

func moveCarry(q rowQuerier, conversation, self string) (MoveCarry, error) {
	relays, err := movedRelays(q, conversation, self)
	if err != nil {
		return MoveCarry{}, err
	}
	channels, err := queryRows(q, []MovedChannel{}, func(row rowScanner) (MovedChannel, error) {
		var channel MovedChannel
		return channel, row.Scan(&channel.Name, &channel.Home)
	}, `SELECT c.name,?2 FROM channel_members m JOIN channels c ON c.id=m.channel_id WHERE m.session_id=?1
		UNION SELECT channel_name,home_host_id FROM remote_channels WHERE session_id=?1
		ORDER BY 1,2`, conversation, self)
	if err != nil {
		return MoveCarry{}, err
	}
	return MoveCarry{Relays: relays, Channels: channels}, nil
}

func (c MoveCarry) equal(other MoveCarry) bool {
	return slices.Equal(c.Relays, other.Relays) && slices.Equal(c.Channels, other.Channels)
}

func movingOutTx(tx *sql.Tx, session string) (bool, error) {
	var moving bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_moves WHERE conversation=? AND role='out' AND state IN ('preparing','sent'))`, session).Scan(&moving)
	return moving, err
}

func (r *Registry) CheckMovedChannels(conversation, self string, channels []MovedChannel) error {
	return checkMovedChannels(r.read, conversation, self, channels)
}

// A channel name names one channel for a session wherever it runs, so a post
// to it has one place to go.
func checkMovedChannels(q catalogQuerier, conversation, self string, channels []MovedChannel) error {
	named := map[string]bool{}
	for _, channel := range channels {
		if channel.Name == "" {
			return fmt.Errorf("a moved channel needs its name")
		}
		if named[channel.Name] {
			return fmt.Errorf("session %s is in two channels named #%s on different hosts; leave one first", conversation, channel.Name)
		}
		named[channel.Name] = true
		if channel.Home == self {
			continue
		}
		if channel.Home == "" {
			return fmt.Errorf("moved channel #%s needs the host that keeps it", channel.Name)
		}
		err := q.QueryRow(`SELECT 1 FROM hosts WHERE host_id=?`, channel.Home).Scan(new(int))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("channel #%s is kept on host %s, which is not paired here; pair it here first", channel.Name, channel.Home)
		}
		if err != nil {
			return err
		}
		var local bool
		if err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM channel_members m JOIN channels c ON c.id=m.channel_id WHERE m.session_id=? AND c.name=?)`, conversation, channel.Name).Scan(&local); err != nil {
			return err
		}
		if local {
			return fmt.Errorf("session %s is in this host's own #%s and in #%s on host %s; leave one first", conversation, channel.Name, channel.Name, channel.Home)
		}
	}
	return nil
}

func channelMoveRefusal(q catalogQuerier, conversation string) (string, error) {
	var name string
	err := q.QueryRow(`SELECT c.name FROM channel_members m JOIN channels c ON c.id=m.channel_id JOIN remote_channels r ON r.session_id=m.session_id AND r.channel_name=c.name WHERE m.session_id=? ORDER BY c.name LIMIT 1`, conversation).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("it is in two channels named #%s on different hosts; leave one first", name), nil
}

// A channel kept here needs no row: its membership never left.
func insertMovedChannelsTx(tx *sql.Tx, conversation, self string, channels []MovedChannel) error {
	if err := checkMovedChannels(tx, conversation, self, channels); err != nil {
		return err
	}
	for _, channel := range channels {
		if channel.Home == self {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO remote_channels(channel_name,home_host_id,session_id) VALUES(?,?,?)`, channel.Name, channel.Home, conversation); err != nil {
			return err
		}
	}
	return nil
}

func dropMovedChannelsTx(tx *sql.Tx, conversation string) error {
	_, err := tx.Exec(`DELETE FROM remote_channels WHERE session_id=?`, conversation)
	return err
}

func (r *Registry) RemoteChannel(session, name string) (string, bool, error) {
	var home string
	err := r.read.QueryRow(`SELECT home_host_id FROM remote_channels WHERE session_id=? AND channel_name=?`, session, name).Scan(&home)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return home, err == nil, err
}

func (r *Registry) DropRemoteChannel(session, name, home string) error {
	_, err := r.db.Exec(`DELETE FROM remote_channels WHERE session_id=? AND channel_name=? AND home_host_id=?`, session, name, home)
	return err
}
