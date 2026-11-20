package daemon

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAnAgentInAProjectReachesAnIdentityHereAndOnAPairedHost(t *testing.T) {
	h := newHostTargets(t, false)
	for node, name := range map[*federationNode]string{h.local: "notes", h.remote: "scout"} {
		withInbox(node)
		if _, err := node.reg.AddAgentIdentity(name); err != nil {
			t.Fatal(err)
		}
		node.daemon.discoverCatalog(context.Background())
	}
	remoteScout := identityID(t, h.remote, "scout")
	testkit.Eventually(t, "the paired host's scout mirrored here", 10*time.Second, func() bool {
		owner, err := h.local.reg.SessionHost(remoteScout)
		return err == nil && owner == h.record.ID
	})

	if out, refused := h.send(t, "vps::@scout", "to the paired host's scout", nil); refused {
		t.Fatalf("an agent in a project could not reach an identity on a paired host: %v", out)
	}
	if got := inbox(t, h.remote, "scout"); len(got) != 1 || !strings.Contains(got[0].Message, "to the paired host's scout") {
		t.Fatalf("the paired host's scout inbox=%+v", got)
	}

	if out, refused := h.send(t, "@notes", "to this host's identity", nil); refused {
		t.Fatalf("an agent in a project could not reach this host's identity: %v", out)
	}
	if got := inbox(t, h.local, "notes"); len(got) != 1 || !strings.Contains(got[0].Message, "to this host's identity") {
		t.Fatalf("this host's identity inbox=%+v", got)
	}

	remoteRef := hosts.Qualify(h.record.ID, remoteScout)
	for _, target := range []string{remoteRef, identityID(t, h.local, "notes")} {
		answer := askScope(t, h.local.daemon, ScopeRequest{SessionID: "local-a", Action: "send", Payload: scopePayload(PersonActionAPI, `{"action":"send","sessionId":"`+target+`","message":"m","busyDelivery":"queue"}`)}, "127.0.0.1:50000")
		if answer.status != http.StatusOK || answer.reply.ScopeProjectID != "" {
			t.Fatalf("the CLI's scope check for a send to %s: %+v body=%s", target, answer, answer.body)
		}
	}

	scoped := h.local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": hosts.Qualify(h.record.ID, remoteScout), "sourceSessionId": "local-a", "scopeProjectId": widgetProject, "message": "scoped to the sender's project"})
	if scoped.Code != http.StatusOK {
		t.Fatalf("a send scoped to the sender's project status=%d body=%s", scoped.Code, scoped.Body.String())
	}
	if got := inbox(t, h.remote, "scout"); len(got) != 2 || !strings.Contains(got[1].Message, "scoped to the sender's project") {
		t.Fatalf("the paired host's scout inbox after a scoped send=%+v", got)
	}

	out, refused := h.send(t, "vps::@scout", "sent now", map[string]any{"mode": "now"})
	refusedFor(t, out, refused, "out_of_scope", "not_queued")
	out, refused = h.send(t, "@notes", "sent now here", map[string]any{"mode": "now"})
	refusedFor(t, out, refused, "out_of_scope", "not_spawned_or_led")
	if answer := askScope(t, h.local.daemon, ScopeRequest{SessionID: "local-a", Action: "control", Target: identityID(t, h.local, "notes")}, "127.0.0.1:50000"); answer.status != http.StatusForbidden || answer.code != "out_of_scope" {
		t.Fatalf("an agent controlling an identity: %+v body=%s", answer, answer.body)
	}
	if got := inbox(t, h.local, "notes"); len(got) != 1 {
		t.Fatalf("a refused send reached the identity: %+v", got)
	}
	out, refused = h.send(t, "vps::remote-b", "to another project", nil)
	refusedFor(t, out, refused, "out_of_scope", "different_project")
}
