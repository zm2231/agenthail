package daemon

import (
	"slices"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestPairedHostsAreListedInIDOrder(t *testing.T) {
	d := &Daemon{federation: &federation{hosts: map[string]*pairedHost{}}}
	ids := []string{"host-e", "host-d", "host-c", "host-b", "host-a"}
	unsorted := make([]*pairedHost, 0, len(ids))
	for _, id := range ids {
		host := &pairedHost{record: registry.PairedHost{ID: id}}
		d.federation.hosts[id] = host
		unsorted = append(unsorted, host)
	}
	slices.Sort(ids)
	listed := func(hosts []*pairedHost) []string {
		var got []string
		for _, host := range hosts {
			got = append(got, host.record.ID)
		}
		return got
	}
	if got := listed(sortHostsByID(unsorted)); !slices.Equal(got, ids) {
		t.Fatalf("hosts given in reverse were ordered %v, want %v", got, ids)
	}
	if got := listed(d.pairedHosts()); !slices.Equal(got, ids) {
		t.Fatalf("paired hosts listed as %v, want %v", got, ids)
	}
}
