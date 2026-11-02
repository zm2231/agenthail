package ledger

import (
	"database/sql"
	"fmt"
	"strings"
)

type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func ProjectSlug(projectKey string) string {
	key := strings.TrimPrefix(projectKey, "repo:")
	key = strings.ReplaceAll(key, "::", "/")
	key = strings.TrimSuffix(strings.TrimRight(key, "/"), ".git")
	if i := strings.LastIndexAny(key, "/:"); i >= 0 {
		key = key[i+1:]
	}
	if key == "" {
		return "ledger"
	}
	return key
}

type idIssuer struct {
	q          querier
	projectKey string
	issued     map[EventType]int
}

func (ids *idIssuer) next(typ EventType, prefix string) (string, error) {
	if ids.issued == nil {
		ids.issued = map[EventType]int{}
	}
	var count int
	if err := ids.q.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND type=?`, ids.projectKey, string(typ)).Scan(&count); err != nil {
		return "", err
	}
	ids.issued[typ]++
	return fmt.Sprintf("%s-%s%d", ProjectSlug(ids.projectKey), prefix, count+ids.issued[typ]), nil
}

func loadEvents(q querier, projectKey string) ([]Event, error) {
	rows, err := q.Query(`SELECT `+eventColumns+` FROM ledger_events WHERE project_key=? ORDER BY seq`, projectKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func loadBoard(q querier, projectKey string) (*Board, error) {
	events, err := loadEvents(q, projectKey)
	if err != nil {
		return nil, err
	}
	board := NewBoard()
	return board, board.Rebuild(events)
}
