package registry

import (
	"encoding/json"
	"fmt"
	"slices"
)

type MoveInReservedError struct {
	Conversation string
	Holds        string
}

func (e MoveInReservedError) Error() string {
	return fmt.Sprintf("session %s is moving here and %s; try again once the move has finished", e.Conversation, e.Holds)
}

type arrivingMove struct {
	Conversation string `json:"conversation"`
	Alias        string `json:"alias"`
	MoveCarry
}

func arrivingMovesTx(q rowQuerier) ([]arrivingMove, error) {
	manifests, err := queryRows(q, [][]byte{}, func(row rowScanner) ([]byte, error) {
		var manifest []byte
		return manifest, row.Scan(&manifest)
	}, `SELECT manifest FROM session_moves WHERE role='in' AND state IN (?,?) ORDER BY id`, MovePrepared, MoveReceived)
	if err != nil {
		return nil, err
	}
	moves := make([]arrivingMove, 0, len(manifests))
	for _, manifest := range manifests {
		var move arrivingMove
		if json.Unmarshal(manifest, &move) == nil {
			moves = append(moves, move)
		}
	}
	return moves, nil
}

func arrivingAliasTx(q rowQuerier, name, sessionID string) error {
	moves, err := arrivingMovesTx(q)
	if err != nil {
		return err
	}
	for _, move := range moves {
		if move.Alias == name && move.Conversation != sessionID {
			return MoveInReservedError{Conversation: move.Conversation, Holds: fmt.Sprintf("keeps its name @%s", name)}
		}
	}
	return nil
}

func arrivingCoordinatorTx(q rowQuerier, projectID, name string) error {
	moves, err := arrivingMovesTx(q)
	if err != nil {
		return err
	}
	for _, move := range moves {
		if lead := move.Coordinator; lead != nil && (lead.ProjectID == projectID || lead.Name == name) {
			return MoveInReservedError{Conversation: move.Conversation, Holds: fmt.Sprintf("coordinates %s as @%s", lead.ProjectID, lead.Name)}
		}
	}
	return nil
}

func arrivingHostTx(q rowQuerier, hostID string) error {
	moves, err := arrivingMovesTx(q)
	if err != nil {
		return err
	}
	for _, move := range moves {
		if index := slices.IndexFunc(move.Channels, func(channel MovedChannel) bool { return channel.Home == hostID }); index >= 0 {
			return MoveInReservedError{Conversation: move.Conversation, Holds: fmt.Sprintf("belongs to channel #%s kept on that host", move.Channels[index].Name)}
		}
		if index := slices.IndexFunc(move.Relays, func(relay MovedRelay) bool { return relay.ToHost == hostID }); index >= 0 {
			return MoveInReservedError{Conversation: move.Conversation, Holds: fmt.Sprintf("relays to session %s on that host", move.Relays[index].ToSession)}
		}
	}
	return nil
}
