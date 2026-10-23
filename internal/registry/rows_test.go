package registry

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestQueryRowsKeepsTheCallersEmptyListWhenNothingMatches(t *testing.T) {
	reg := openTestRegistry(t)
	kinds, err := queryRows(reg.read, []string{}, scanString, `SELECT kind FROM delivery_history WHERE 0`)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(kinds)
	if string(encoded) != "[]" {
		t.Fatalf("an empty listing encodes as %s, want []", encoded)
	}
}

func TestQueryRowsReturnsTheFirstRowError(t *testing.T) {
	reg := openTestRegistry(t)
	refused := errors.New("refused")
	got, err := queryRows(reg.read, []int{}, func(row rowScanner) (int, error) {
		var value int
		if err := row.Scan(&value); err != nil {
			return 0, err
		}
		if value == 2 {
			return 0, refused
		}
		return value, nil
	}, `SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3`)
	if !errors.Is(err, refused) || got != nil {
		t.Fatalf("got %v, %v; want nil, %v", got, err, refused)
	}
}
