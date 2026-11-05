package registry

import (
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestProjectLinksKeepTheLaterRecordWhicheverOrderTheyArrive(t *testing.T) {
	const fork, upstream = "repo:github.com/example/fork", "repo:github.com/example/upstream"
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	linked := ProjectLink{Joined: fork, Survivor: upstream, Linked: true, ChangedAt: at, OriginHost: "h-a"}
	unlinked := ProjectLink{Joined: fork, Survivor: upstream, Linked: false, ChangedAt: at.Add(time.Second), OriginHost: "h-b"}
	tie := ProjectLink{Joined: fork, Survivor: upstream, Linked: true, ChangedAt: at.Add(time.Second), OriginHost: "h-a"}
	local := ProjectLink{Joined: "implicit:/home/me/notes", Survivor: upstream, Linked: true, ChangedAt: at, OriginHost: "h-a"}
	for name, order := range map[string][]ProjectLink{
		"link then unlink": {linked, unlinked, tie},
		"unlink then link": {tie, unlinked, linked},
	} {
		t.Run(name, func(t *testing.T) {
			r := openTestRegistry(t)
			for _, link := range append(order, local) {
				if _, err := r.AdoptProjectLinks([]ProjectLink{link}); err != nil {
					t.Fatal(err)
				}
			}
			links, err := r.ProjectLinks()
			if err != nil || len(links) != 1 || links[0].Linked || links[0].OriginHost != "h-b" {
				t.Fatalf("links=%+v err=%v; want only the unlink from h-b, the later record", links, err)
			}
			r.now = func() time.Time { return at }
			relinked, err := r.SetProjectLink(fork, upstream, true)
			if err != nil || !relinked.ChangedAt.After(unlinked.ChangedAt) {
				t.Fatalf("a link made on a host whose clock is behind lost to the record it replaces: %+v err=%v", relinked, err)
			}
		})
	}
}

func TestAProjectWhoseLedgerWasEnabledHoldsHistoryALinkWouldStrand(t *testing.T) {
	const fork = "repo:github.com/example/fork"
	r := openTestRegistry(t)
	if held, err := r.ProjectHistory(fork); err != nil || len(held) != 0 {
		t.Fatalf("a fresh project holds %v (err %v)", held, err)
	}
	if err := ledger.NewStore(r.WriteDB(), r.ReadDB()).Enable(fork); err != nil {
		t.Fatal(err)
	}
	if held, err := r.ProjectHistory(fork); err != nil || !slices.Equal(held, []string{"a ledger board"}) {
		t.Fatalf("an enabled project holds %v (err %v), want its ledger board", held, err)
	}
}
