package registry

import (
	"testing"
	"time"
)

func TestBacklogItemsAlreadyToldStayToldWhenTheRepositoryKeyMoves(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.ObserveRepositoryKey("/work/widget", "repo:old"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MarkBacklogTold("repo:old", []string{"W1"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if outcome, err := r.ObserveRepositoryKey("/work/widget", "repo:new"); err != nil || !outcome.Moved {
		t.Fatalf("the key change answered %+v (%v), want a move", outcome, err)
	}
	if told, err := r.BacklogTold("repo:new"); err != nil || !told["W1"] {
		t.Fatalf("told under the new key: %v (%v), want W1", told, err)
	}
	if told, err := r.BacklogTold("repo:old"); err != nil || len(told) != 0 {
		t.Fatalf("told under the old key: %v (%v), want none", told, err)
	}
}
