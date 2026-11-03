package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestASendToAProjectReachesItsLeadOnAPairedHostWithTheSendersOptions(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	for node, name := range map[*federationNode]string{local: "Ada at her Mac", remote: "root"} {
		if err := node.reg.SetPersonName(name); err != nil {
			t.Fatal(err)
		}
	}
	gadget := widgetRepo(t, "https://github.com/example/gadget.git")
	moveSession(t, remote, "remote-a", gadget, time.Now())
	setLead(t, remote, "repo:github.com/example/gadget", gadget, "gadget-lead", "remote-a", registry.CoordinatorChange{})
	remote.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the paired host's project and coordinator mirrored", 10*time.Second, func() bool {
		spec, err := local.daemon.projectLead("gadget")
		return err == nil && spec.HostID != ""
	})

	body := `{"action":"coordinator-send","project":"gadget","message":"ship it","author":"person","personName":"Someone Else","model":"gpt-x"}`
	reply := local.daemon.performAction(context.Background(), principal{ID: localDashboardPrincipal, Kind: principalPerson}, actionFormatAPIv1, []byte(body), "")
	var answer coordinatorSendReply
	if reply.Status != http.StatusOK || json.Unmarshal(reply.Body, &answer) != nil || answer.Ref != "vps::@gadget-lead" || answer.Result == nil {
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
