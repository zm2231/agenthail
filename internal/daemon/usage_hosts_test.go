package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestUsageIncludesEveryConnectedHostAndSkipsCodexWhenItIsDown(t *testing.T) {
	local, remote, record := pairedFederation(t)
	fetched := time.Now().Add(-10 * time.Minute).UTC()
	remote.fake.mu.Lock()
	remote.fake.usage = &surface.Usage{FetchedAt: fetched, Limits: []surface.UsageWindow{{Kind: "weekly", Percent: 19, ResetsAt: fetched.Add(72 * time.Hour)}}}
	remote.fake.mu.Unlock()

	read := func(node *federationNode, path string) []usageSet {
		response := node.request(t, http.MethodGet, path, nil)
		var body usageBody
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		return body.Sets
	}
	remoteCodex := func(sets []usageSet) *usageSet {
		for i, set := range sets {
			if set.HostID == record.ID && set.Provider == "codex" {
				return &sets[i]
			}
		}
		return nil
	}

	all := read(local, "/api/v1/usage")
	codex := remoteCodex(all)
	if codex == nil || codex.Host != record.Name || codex.Local || len(codex.Meters) != 1 || codex.Meters[0].Label != "Codex week" || codex.Meters[0].Percent != 19 {
		t.Fatalf("the remote host's Codex set is missing or unnamed: %+v", all)
	}
	for _, set := range all {
		if set.HostID != record.ID && set.Provider == "codex" && len(set.Meters) > 0 {
			t.Fatalf("the local host answered with the remote's usage: %+v", set)
		}
	}
	if sets := read(local, "/api/v1/usage?scope=local"); remoteCodex(sets) != nil {
		t.Fatalf("scope=local reached another host: %+v", sets)
	}

	if _, _, err := remote.reg.RecordCatalogSurface(registry.CatalogSurfaceState{Surface: surface.KindCodex, Health: "unavailable"}, registry.CatalogEvent{DedupeKey: "surface:codex:unavailable", Type: "surface.health", EntityID: string(surface.KindCodex), Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if sets := read(local, "/api/v1/usage"); remoteCodex(sets) != nil {
		t.Fatalf("a Codex that is down still answered usage: %+v", sets)
	}
}

func TestHostUsageProblemSaysWhyAHostHasNoSets(t *testing.T) {
	older := hostUsageProblem("vps", hosts.StatusError{Status: http.StatusNotFound, Code: "unknown_route", Message: "This Agenthail has no /api/v1/usage"})
	if !strings.Contains(older, "older Agenthail") || !strings.HasPrefix(older, "vps") {
		t.Fatalf("older=%q", older)
	}
	if slow := hostUsageProblem("vps", context.DeadlineExceeded); slow != "vps did not answer in time." {
		t.Fatalf("slow=%q", slow)
	}
	if other := hostUsageProblem("vps", errors.New("connection refused")); other != "vps: connection refused" {
		t.Fatalf("other=%q", other)
	}
}
