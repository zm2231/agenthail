package daemon

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
)

// A host's answer includes its own claude.ai reads.
const remoteUsageTimeout = claudeRootUsageTimeout + time.Second

func (d *Daemon) remoteUsageSets(ctx context.Context) []usageSet {
	connected := []*pairedHost{}
	for _, host := range d.pairedHosts() {
		if state, _ := host.status(); state == hostStateConnected {
			connected = append(connected, host)
		}
	}
	sort.Slice(connected, func(i, j int) bool { return connected[i].record.Name < connected[j].record.Name })
	results := make([][]usageSet, len(connected))
	var wait sync.WaitGroup
	for i, host := range connected {
		wait.Go(func() { results[i] = hostUsageSets(ctx, host) })
	}
	wait.Wait()
	return slices.Concat(results...)
}

func hostUsageSets(ctx context.Context, host *pairedHost) []usageSet {
	ctx, cancel := context.WithTimeout(ctx, remoteUsageTimeout)
	defer cancel()
	name, id := host.record.Name, host.record.ID
	var body struct {
		Sets []usageSet `json:"sets"`
	}
	if err := host.client.GetJSON(ctx, "/api/v1/usage", url.Values{"scope": {"local"}}, &body); err != nil {
		return []usageSet{{Host: name, HostID: id, Problem: hostUsageProblem(name, err)}}
	}
	for i := range body.Sets {
		body.Sets[i].Host, body.Sets[i].HostID, body.Sets[i].Local = name, id, false
	}
	return body.Sets
}

func hostUsageProblem(name string, err error) string {
	var status hosts.StatusError
	switch {
	case errors.As(err, &status) && status.Code == "unknown_route":
		return name + " runs an older Agenthail. Update it to see its usage."
	case errors.Is(err, context.DeadlineExceeded):
		return name + " did not answer in time."
	}
	return name + ": " + err.Error()
}
