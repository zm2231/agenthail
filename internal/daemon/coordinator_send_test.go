package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func gadgetLeadOnRemote(t *testing.T, local, remote *federationNode, sessionID string) {
	t.Helper()
	gadget := widgetRepo(t, "https://github.com/example/gadget.git")
	moveSession(t, remote, "remote-a", gadget, time.Now())
	setLead(t, remote, "repo:github.com/example/gadget", gadget, "gadget-lead", sessionID, registry.CoordinatorChange{})
	remote.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the paired host's project and coordinator mirrored", 10*time.Second, func() bool {
		spec, err := local.daemon.projectLead("gadget")
		return err == nil && spec.HostID != ""
	})
}

func sendAsPerson(d *Daemon, body string) actionReply {
	return d.performAction(context.Background(), principal{ID: localDashboardPrincipal, Kind: principalPerson}, actionFormatAPIv1, []byte(body), "")
}

func TestASendToAProjectReachesItsLeadOnAPairedHostWithTheSendersOptions(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	for node, name := range map[*federationNode]string{local: "Ada at her Mac", remote: "root"} {
		if err := node.reg.SetPersonName(name); err != nil {
			t.Fatal(err)
		}
	}
	gadgetLeadOnRemote(t, local, remote, "remote-a")

	reply := sendAsPerson(local.daemon, `{"action":"coordinator-send","project":"gadget","message":"ship it","author":"person","personName":"Someone Else","model":"gpt-x"}`)
	var answer coordinatorSendReply
	if reply.Status != http.StatusOK || json.Unmarshal(reply.Body, &answer) != nil || answer.Ref != "vps::@gadget-lead" || answer.Result == nil || answer.Started {
		t.Fatalf("send to the gadget lead: %d %s", reply.Status, reply.Body)
	}
	remote.fake.mu.Lock()
	defer remote.fake.mu.Unlock()
	if len(remote.fake.options) == 0 {
		t.Fatalf("the paired host delivered nothing: %q", remote.fake.messages)
	}
	got, person := remote.fake.options[len(remote.fake.options)-1], remote.fake.persons[len(remote.fake.persons)-1]
	if got.Model != "gpt-x" || got.Author != surface.AuthorPerson || person != "Ada at her Mac" {
		t.Fatalf("the paired host delivered with %+v as %q, want the sender's model, the person as author, and the name their own host has for them", got, person)
	}
}

func TestASendToAPairedHostsUnstartedLeadSaysItStartedIt(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	gadgetLeadOnRemote(t, local, remote, "")
	started := len(remote.fake.startOptions)

	reply := sendAsPerson(local.daemon, `{"action":"coordinator-send","project":"gadget","message":"begin"}`)
	var answer coordinatorSendReply
	if reply.Status != http.StatusOK || json.Unmarshal(reply.Body, &answer) != nil || !answer.Started || answer.Ref != "vps::@gadget-lead" {
		t.Fatalf("send to an unstarted lead: %d %s", reply.Status, reply.Body)
	}
	if len(remote.fake.startOptions) != started+1 {
		t.Fatalf("the paired host started %d conversations, want 1", len(remote.fake.startOptions)-started)
	}
}

func TestAPairedHostsSendIsNotPassedOnToAThirdHostsLead(t *testing.T) {
	middle, remote, _ := pairedFederation(t)
	gadgetLeadOnRemote(t, middle, remote, "remote-a")
	middle.serve(t, nil)
	front := newFederationNode(t, "front-a")
	front.start(t, testRoots(middle.server))
	pairing := front.pair(t, middle, "mid")
	testkit.Eventually(t, "the middle host's sessions mirrored", 10*time.Second, func() bool {
		return len(sessionsOf(front.snapshot(t), pairing.ID)) > 0
	})
	delivered, started := len(remote.fake.delivered()), len(remote.fake.startOptions)

	for _, request := range []map[string]any{
		{"action": "coordinator-send", "host": "mid", "project": "gadget", "message": "as the person", "author": "person"},
		{"action": "coordinator-send", "host": "mid", "project": "gadget", "message": "as a session", "sourceSessionId": "front-a"},
	} {
		response := front.request(t, http.MethodPost, "/api/v1/actions", request)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "host_transit") {
			t.Fatalf("%v through the middle host: %d %s", request["message"], response.Code, response.Body)
		}
	}
	if len(remote.fake.delivered()) != delivered || len(remote.fake.startOptions) != started {
		t.Fatalf("the third host received %q", remote.fake.delivered()[delivered:])
	}
}
