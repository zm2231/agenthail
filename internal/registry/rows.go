package registry

import "database/sql"

type rowScanner interface {
	Scan(...any) error
}

type rowQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func queryRows[T any](q rowQuerier, dst []T, scan func(rowScanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return appendRows(dst, rows, scan)
}

// appendRows closes rows before it returns, so a transaction can run its next
// statement on the same connection.
func appendRows[T any](dst []T, rows *sql.Rows, scan func(rowScanner) (T, error)) ([]T, error) {
	defer rows.Close()
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		dst = append(dst, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dst, rows.Close()
}

func scanString(row rowScanner) (string, error) {
	var value string
	err := row.Scan(&value)
	return value, err
}
