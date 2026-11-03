package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type threeHosts struct {
	front, middle, far *federationNode
	farRecord          registry.PairedHost
	viaMiddle          *pairedHost
}

func pairThreeHosts(t *testing.T) threeHosts {
	t.Helper()
	middle, far, farRecord := pairedFederation(t)
	middle.serve(t, nil)
	front := newFederationNode(t, "front-a")
	front.start(t, testRoots(middle.server))
	pairing := front.pair(t, middle, "mid")
	testkit.Eventually(t, "the middle host's sessions mirrored", 10*time.Second, func() bool {
		return len(sessionsOf(front.snapshot(t), pairing.ID)) > 0
	})
	viaMiddle, ok := front.daemon.pairedHost("mid")
	if !ok {
		t.Fatal("front has no paired host mid")
	}
	return threeHosts{front: front, middle: middle, far: far, farRecord: farRecord, viaMiddle: viaMiddle}
}

func (h threeHosts) ask(t *testing.T, method, path string, query url.Values, body any) (int, string) {
	t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	response, err := h.viaMiddle.client.Do(context.Background(), method, path, query, data, nil)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(payload)
}

func refusedInTransit(t *testing.T, what string, status int, body string) {
	t.Helper()
	if status != http.StatusConflict || !strings.Contains(body, "host_transit") {
		t.Fatalf("%s through the middle host: %d %s", what, status, body)
	}
}

func TestAPairedHostsActionIsNotPassedOnToAThirdHost(t *testing.T) {
	h := pairThreeHosts(t)
	delivered := len(h.far.fake.delivered())
	status, body := h.ask(t, http.MethodPost, "/api/v1/actions", nil, map[string]any{"action": "send", "sessionId": hosts.Qualify(h.farRecord.ID, "remote-a"), "message": "through two hosts", "author": "person"})
	refusedInTransit(t, "a send", status, body)
	if got := h.far.fake.delivered(); len(got) != delivered {
		t.Fatalf("the third host received %q", got[delivered:])
	}
}

func TestAPairedHostsSessionReadIsNotPassedOnToAThirdHost(t *testing.T) {
	h := pairThreeHosts(t)
	status, body := h.ask(t, http.MethodGet, "/api/v1/session", url.Values{"id": {hosts.Qualify(h.farRecord.ID, "remote-a")}}, nil)
	refusedInTransit(t, "a session read", status, body)
}

func TestAPairedHostCannotResolveTargetsOnAThirdHost(t *testing.T) {
	h := pairThreeHosts(t)
	status, body := h.ask(t, http.MethodGet, "/api/v1/resolve", url.Values{"target": {"remote-a"}, "scope": {"remote"}}, nil)
	refusedInTransit(t, "a remote resolve", status, body)
}

func TestAPairedHostReadsOnlyThisHostsUsage(t *testing.T) {
	h := pairThreeHosts(t)
	h.far.fake.mu.Lock()
	h.far.fake.usage = &surface.Usage{FetchedAt: time.Now(), Limits: []surface.UsageWindow{{Kind: "weekly", Percent: 19, ResetsAt: time.Now().Add(72 * time.Hour)}}}
	h.far.fake.mu.Unlock()
	status, body := h.ask(t, http.MethodGet, "/api/v1/usage", nil, nil)
	var answer usageBody
	if status != http.StatusOK || json.Unmarshal([]byte(body), &answer) != nil {
		t.Fatalf("usage through the middle host: %d %s", status, body)
	}
	for _, set := range answer.Sets {
		if set.HostID == h.farRecord.ID {
			t.Fatalf("the middle host answered with the third host's usage: %+v", set)
		}
	}
}

func TestAPairedHostCannotReplaceAThirdHostsCoordinator(t *testing.T) {
	h := pairThreeHosts(t)
	const project = "repo:github.com/example/gadget"
	farGadget := widgetRepo(t, "https://github.com/example/gadget.git")
	moveSession(t, h.far, "remote-a", farGadget, time.Now())
	setLead(t, h.far, project, farGadget, "gadget-lead", "remote-a", registry.CoordinatorChange{})
	moveSession(t, h.middle, "local-a", widgetRepo(t, "https://github.com/example/gadget.git"), time.Now())
	h.far.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the middle host mirrors the third host's coordinator", 10*time.Second, func() bool {
		mirrored, _ := h.middle.reg.ProjectCoordinators(project)
		return len(mirrored) == 1 && mirrored[0].HostID == h.farRecord.ID
	})

	status, body := h.ask(t, http.MethodPost, "/api/v1/actions", nil, map[string]any{"action": "coordinator-set", "sessionId": "local-a", "name": "mid-lead", "replace": true})
	refusedInTransit(t, "a coordinator replace", status, body)
	if kept, err := h.far.reg.Coordinator(project); err != nil || kept.Name != "gadget-lead" {
		t.Fatalf("the third host's coordinator=%+v err=%v", kept, err)
	}
	if _, err := h.middle.reg.Coordinator(project); err == nil {
		t.Fatal("the middle host took the coordinator anyway")
	}
}
