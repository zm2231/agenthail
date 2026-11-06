package sessionstream

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrResync means an append did not start where this connection's copy of
// its row ends; the stream has to be opened again from the last applied seq.
var ErrResync = errors.New("session stream append does not continue the row it names")

// Rebuild turns one connection's stream envelopes back into whole rows. A
// stream sends a row whole the first time the connection sees it, and only
// the bytes it gained after that.
type Rebuild struct {
	rows  map[string]rebuildRow
	clock uint64
}

type rebuildRow struct {
	event   Event
	touched uint64
}

// RebuildRowLimit bounds the rows one connection remembers. A row let go that
// is appended to again asks for a resync, which sends it whole.
const RebuildRowLimit = 512

func NewRebuild() *Rebuild {
	return &Rebuild{rows: map[string]rebuildRow{}}
}

func (b *Rebuild) keep(event Event) {
	b.clock++
	b.rows[event.ItemID] = rebuildRow{event: event, touched: b.clock}
	if len(b.rows) <= RebuildRowLimit {
		return
	}
	oldest, at := "", b.clock
	for id, row := range b.rows {
		if row.touched < at {
			oldest, at = id, row.touched
		}
	}
	delete(b.rows, oldest)
}

func (b *Rebuild) Apply(seq uint64, envelope []byte) (Event, error) {
	var message struct {
		Seq     uint64          `json:"seq"`
		Type    string          `json:"type"`
		Data    json.RawMessage `json:"data"`
		ItemID  string          `json:"itemId"`
		From    int             `json:"from"`
		Text    string          `json:"text"`
		Version uint64          `json:"version"`
		Final   bool            `json:"final"`
	}
	if err := json.Unmarshal(envelope, &message); err != nil {
		return Event{}, fmt.Errorf("decode session stream envelope: %w", err)
	}
	if message.Seq != 0 {
		seq = message.Seq
	}
	if message.Type == "append" {
		held, found := b.rows[message.ItemID]
		if !found || len(held.event.Body) != message.From {
			return Event{}, ErrResync
		}
		row := held.event
		row.Seq, row.Version, row.Final, row.Truncated = seq, message.Version, message.Final, false
		row.Body += message.Text
		b.keep(row)
		return row, nil
	}
	event, err := DecodePayload(seq, message.Data)
	if err != nil {
		return Event{}, err
	}
	if event.ItemID != "" {
		if event.Operation == "remove" {
			delete(b.rows, event.ItemID)
		} else {
			b.keep(event)
		}
	}
	return event, nil
}
