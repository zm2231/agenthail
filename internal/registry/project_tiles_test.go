package registry

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestTheTileOrderKeepsEveryIdGivenAndRefusesAWriteAgainstAnOlderRevision(t *testing.T) {
	r := openTestRegistry(t)
	empty, err := r.TileOrder()
	if err != nil || empty.Revision != 0 || empty.Order == nil || empty.Pinned == nil || len(empty.Order)+len(empty.Pinned) != 0 {
		t.Fatalf("an unset order = %+v err=%v; want empty lists at revision 0", empty, err)
	}
	first, err := r.SetTileOrder([]string{"repo:github.com/example/app", "other-host::implicit:/srv/notes", "", "repo:github.com/example/app", "implicit:/home/me/site"}, []string{"implicit:/home/me/site"}, 0)
	if err != nil || first.Revision != 1 {
		t.Fatalf("first write = %+v err=%v", first, err)
	}
	read, err := r.TileOrder()
	if err != nil || !slices.Equal(read.Order, []string{"repo:github.com/example/app", "other-host::implicit:/srv/notes", "implicit:/home/me/site"}) || !slices.Equal(read.Pinned, []string{"implicit:/home/me/site"}) || read.Revision != 1 {
		t.Fatalf("read back %+v err=%v; want every id once, unknown ones kept, in the order given", read, err)
	}
	_, err = r.SetTileOrder([]string{"implicit:/home/me/site"}, nil, 0)
	var stale *StaleTileOrderError
	if !errors.As(err, &stale) || stale.Current.Revision != 1 || !slices.Equal(stale.Current.Order, read.Order) {
		t.Fatalf("a write against revision 0 after revision 1 = %v; want a stale error carrying the current order", err)
	}
	second, err := r.SetTileOrder([]string{"implicit:/home/me/site"}, nil, 1)
	if err != nil || second.Revision != 2 || len(second.Pinned) != 0 {
		t.Fatalf("second write = %+v err=%v", second, err)
	}
}

func TestAMovedProjectKeepsItsTilePlace(t *testing.T) {
	r := openTestRegistry(t)
	const from, to = "repo:github.com/example/old", "repo:github.com/example/new"
	if _, err := r.SetTileOrder([]string{"implicit:/home/me/site", from}, []string{from}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AdoptProjectMoves([]ProjectMove{{From: from, To: to, ChangedAt: time.Now().Add(time.Minute).Truncate(time.Millisecond), OriginHost: "h-a"}}); err != nil {
		t.Fatal(err)
	}
	tiles, err := r.TileOrder()
	if err != nil || !slices.Equal(tiles.Order, []string{"implicit:/home/me/site", to}) || !slices.Equal(tiles.Pinned, []string{to}) || tiles.Revision != 2 {
		t.Fatalf("after the move %+v err=%v; want the tile in its place and pinned under the new key", tiles, err)
	}
}
