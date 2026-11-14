package daemon

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/testkit"
)

func briefOf(t *testing.T, reply actionReply) coordinatorSendReply {
	t.Helper()
	var answer coordinatorSendReply
	if reply.Status != http.StatusOK || json.Unmarshal(reply.Body, &answer) != nil || answer.Result == nil {
		t.Fatalf("brief: %d %s", reply.Status, reply.Body)
	}
	return answer
}

func TestABriefAsksAPairedHostsLeadToCatchThePersonUpSinceTheyLastWroteToIt(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	gadgetLeadOnRemote(t, local, remote, "remote-a")

	first := briefOf(t, sendAsPerson(local.daemon, `{"action":"coordinator-brief","project":"gadget","author":"person"}`))
	if first.Ref != "vps::@gadget-lead" || first.Since == nil || !first.Since.Fallback || time.Since(first.Since.At) < 23*time.Hour {
		t.Fatalf("a brief with no earlier message from the person: %+v since %+v", first, first.Since)
	}
	asked := remote.fake.delivered()
	if len(asked) == 0 || !strings.Contains(asked[len(asked)-1], "last 24 hours") {
		t.Fatalf("the lead was asked %q, want the last 24 hours", asked)
	}

	sentAt := time.Now().Add(-time.Second)
	if reply := sendAsPerson(local.daemon, `{"action":"coordinator-send","project":"gadget","message":"ship   the\nrelease","author":"person"}`); reply.Status != http.StatusOK {
		t.Fatalf("person's message: %d %s", reply.Status, reply.Body)
	}
	if reply := sendAsPerson(local.daemon, `{"action":"coordinator-send","project":"gadget","message":"an agent's note","sourceSessionId":"local-a"}`); reply.Status != http.StatusOK {
		t.Fatalf("agent's message: %d %s", reply.Status, reply.Body)
	}
	second := briefOf(t, sendAsPerson(local.daemon, `{"action":"coordinator-brief","project":"gadget","sourceSessionId":"local-a"}`))
	if second.Since == nil || second.Since.Fallback || second.Since.At.Before(sentAt.Truncate(time.Second)) || second.Since.At.After(time.Now()) {
		t.Fatalf("a brief after the person wrote: since %+v, want the person's message at about %s", second.Since, sentAt)
	}
	asked = remote.fake.delivered()
	if got := asked[len(asked)-1]; !strings.Contains(got, `"ship the release"`) || !strings.Contains(got, second.Since.At.Format(time.RFC3339)) || strings.Contains(got, "an agent's note") {
		t.Fatalf("the lead was asked %q, want the window to start at the person's message and quote it, not the agent's", got)
	}

	third := briefOf(t, sendAsPerson(local.daemon, `{"action":"coordinator-brief","project":"gadget","author":"person"}`))
	if third.Since == nil || third.Since.Fallback || !third.Since.At.Equal(second.Since.At) {
		t.Fatalf("a brief after an agent asked for one: since %+v, want the person's message %+v", third.Since, second.Since)
	}
	fourth := briefOf(t, sendAsPerson(local.daemon, `{"action":"coordinator-brief","project":"gadget","author":"person"}`))
	asked = remote.fake.delivered()
	if fourth.Since == nil || !fourth.Since.At.After(third.Since.At) || !strings.Contains(asked[len(asked)-1], `began: "Catch me up`) {
		t.Fatalf("the person's own brief starts the next window: since %+v, asked %q", fourth.Since, asked[len(asked)-1])
	}
}

func TestTheLeadCannotAskItselfForABriefAndABriefTakesNoMessage(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	gadgetLeadOnRemote(t, local, remote, "remote-a")
	delivered := len(remote.fake.delivered())

	for body, want := range map[string]string{
		`{"action":"coordinator-brief","name":"gadget-lead","sourceSessionId":"remote-a"}`:     "you are this project's lead",
		`{"action":"coordinator-brief","name":"gadget-lead","message":"hi","author":"person"}`: "a brief takes no message",
	} {
		reply := sendAsPerson(remote.daemon, body)
		if reply.Status != http.StatusBadRequest || !strings.Contains(string(reply.Body), want) {
			t.Fatalf("%s: %d %s, want %q", body, reply.Status, reply.Body, want)
		}
	}
	if got := remote.fake.delivered(); len(got) != delivered {
		t.Fatalf("a refused brief reached the lead: %q", got[delivered:])
	}
}

func TestTheProjectBriefRouteAsksTheLeadOnItsHost(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	gadgetLeadOnRemote(t, local, remote, "remote-a")
	delivered := len(remote.fake.delivered())

	remoteID := mustHostID(t, remote)
	for _, project := range []string{"gadget", hosts.Qualify(remoteID, "repo:github.com/example/gadget")} {
		response := local.request(t, http.MethodPost, "/api/v1/projects/"+url.PathEscape(project)+"/brief", nil)
		var answer coordinatorSendReply
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil || answer.Ref != "vps::@gadget-lead" || answer.Since == nil {
			t.Fatalf("brief route for %s: %d %s", project, response.Code, response.Body)
		}
		delivered++
		if got := remote.fake.delivered(); len(got) != delivered || !strings.HasPrefix(got[delivered-1], "Catch me up") {
			t.Fatalf("the lead was asked %q for %s", got, project)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/gadget/brief", bytes.NewReader([]byte(`{"message":"hi"}`)))
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	request.AddCookie(browserCookie(t, local.reg))
	response := httptest.NewRecorder()
	local.handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || len(remote.fake.delivered()) != delivered {
		t.Fatalf("a brief with a body: %d %s", response.Code, response.Body)
	}
}

func TestABriefHeldForAHostItsLeadLeftIsAskedOfTheLeadOnTheHostItMovedTo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := newFederationNode(t, "old-a")
	old.serve(t, nil)
	next := newFederationNode(t, "next-a")
	next.serve(t, nil)
	local := newFederationNode(t, "local-a")
	roots := x509.NewCertPool()
	roots.AddCert(old.server.Certificate())
	roots.AddCert(next.server.Certificate())
	local.start(t, roots)
	oldRecord := local.pair(t, old, "old")
	local.pair(t, next, "next")
	gadgetLeadOnRemote(t, local, next, "next-a")
	nextID := mustHostID(t, next)
	old.gate.movedTo.Store(&nextID)

	body := []byte(`{"action":"coordinator-brief","name":"gadget-lead","author":"person"}`)
	if _, err := local.reg.HoldHostAction(oldRecord.ID, hostActionKey("moved-chosen-by-caller", oldRecord.ID), "@gadget-lead", body, false); err != nil {
		t.Fatal(err)
	}
	oldHost, _ := local.daemon.pairedHost(oldRecord.ID)
	nextHost, _ := local.daemon.pairedHost(nextID)
	testkit.Eventually(t, "the held brief asked of the lead where it moved", 10*time.Second, func() bool {
		local.daemon.drainHostDeliveries(context.Background(), oldHost)
		local.daemon.drainHostDeliveries(context.Background(), nextHost)
		for _, message := range next.fake.delivered() {
			if strings.HasPrefix(message, "Catch me up") {
				return true
			}
		}
		return false
	})
	for _, id := range []string{oldRecord.ID, nextID} {
		if held, err := local.reg.HostHeldActions(id); err != nil || len(held) != 0 {
			t.Fatalf("held on %s: %+v %v", id, held, err)
		}
	}
	if got := old.fake.delivered(); len(got) != 0 {
		t.Fatalf("the host the lead left was asked %q", got)
	}

	oldID := oldRecord.ID
	next.gate.movedTo.Store(&oldID)
	if _, err := local.reg.HoldHostAction(oldRecord.ID, "brief-bounced:"+oldRecord.ID, "@gadget-lead", body, false); err != nil {
		t.Fatal(err)
	}
	asked := len(next.fake.delivered())
	for range 3 {
		local.daemon.drainHostDeliveries(context.Background(), oldHost)
		local.daemon.drainHostDeliveries(context.Background(), nextHost)
	}
	for _, id := range []string{oldRecord.ID, nextID} {
		if held, err := local.reg.HostHeldActions(id); err != nil || len(held) != 0 {
			t.Fatalf("a brief two hosts each call moved is still held on %s: %+v %v", id, held, err)
		}
	}
	if got := next.fake.delivered(); len(got) != asked {
		t.Fatalf("a bounced brief reached a lead: %q", got[asked:])
	}
}
