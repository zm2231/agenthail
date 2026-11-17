package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
)

// Ids this host does not know, such as another host's folders, are kept.
type TileOrder struct {
	Order    []string `json:"order"`
	Pinned   []string `json:"pinned"`
	Revision int64    `json:"revision"`
}

type StaleTileOrderError struct{ Current TileOrder }

func (e *StaleTileOrderError) Error() string {
	return "the tile order changed since it was read"
}

func (r *Registry) TileOrder() (TileOrder, error) {
	return tileOrderTx(r.read)
}

func (r *Registry) SetTileOrder(order, pinned []string, revision int64) (TileOrder, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return TileOrder{}, err
	}
	defer tx.Rollback()
	current, err := tileOrderTx(tx)
	if err != nil {
		return TileOrder{}, err
	}
	if revision != current.Revision {
		return TileOrder{}, &StaleTileOrderError{Current: current}
	}
	next := TileOrder{Order: tileIDs(order), Pinned: tileIDs(pinned), Revision: current.Revision + 1}
	if err := putTileOrderTx(tx, next); err != nil {
		return TileOrder{}, err
	}
	return next, tx.Commit()
}

func tileOrderTx(q interface{ QueryRow(string, ...any) *sql.Row }) (TileOrder, error) {
	var order, pinned string
	tiles := TileOrder{Order: []string{}, Pinned: []string{}}
	err := q.QueryRow(`SELECT order_json,pinned_json,revision FROM project_tiles WHERE id=1`).Scan(&order, &pinned, &tiles.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return tiles, nil
	}
	if err != nil {
		return TileOrder{}, err
	}
	if err := json.Unmarshal([]byte(order), &tiles.Order); err != nil {
		return TileOrder{}, err
	}
	if err := json.Unmarshal([]byte(pinned), &tiles.Pinned); err != nil {
		return TileOrder{}, err
	}
	return tiles, nil
}

func putTileOrderTx(tx *sql.Tx, tiles TileOrder) error {
	order, err := json.Marshal(tiles.Order)
	if err != nil {
		return err
	}
	pinned, err := json.Marshal(tiles.Pinned)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO project_tiles(id,order_json,pinned_json,revision) VALUES(1,?,?,?)
		ON CONFLICT(id) DO UPDATE SET order_json=excluded.order_json, pinned_json=excluded.pinned_json, revision=excluded.revision`,
		string(order), string(pinned), tiles.Revision)
	return err
}

func tileIDs(ids []string) []string {
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !slices.Contains(kept, id) {
			kept = append(kept, id)
		}
	}
	return kept
}

func moveProjectTilesTx(tx *sql.Tx, move ProjectMove) error {
	tiles, err := tileOrderTx(tx)
	if err != nil {
		return err
	}
	if !slices.Contains(tiles.Order, move.From) && !slices.Contains(tiles.Pinned, move.From) {
		return nil
	}
	rename := func(ids []string) []string {
		renamed := slices.Clone(ids)
		for i, id := range renamed {
			if id == move.From {
				renamed[i] = move.To
			}
		}
		return tileIDs(renamed)
	}
	tiles.Order, tiles.Pinned, tiles.Revision = rename(tiles.Order), rename(tiles.Pinned), tiles.Revision+1
	return putTileOrderTx(tx, tiles)
}
