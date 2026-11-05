package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/testkit"
)

func captureOf(t *testing.T, node *federationNode, session, prompt, text string) ledgerCaptureResponse {
	t.Helper()
	var captured ledgerCaptureResponse
	mustLedger(t, node, "/api/v1/ledger/capture", ledgerCaptureRequest{SessionID: session, PromptID: prompt, Prompt: text}, &captured)
	return captured
}

func TestTheLeadOfAGitProjectIsCapturedWithoutBeingAskedToAttach(t *testing.T) {
	mac, _, _ := widgetOnTwoHosts(t)
	if got := captureOf(t, mac, "lead", "p-1", "build search"); !got.Captured || got.Attach || got.ProjectKey != widgetKey || len(got.Unattached) != 0 {
		t.Fatalf("the lead's words in a project nobody enabled: %+v", got)
	}
	if got := captureOf(t, mac, "local-a", "p-2", "not the lead"); got.Captured {
		t.Fatalf("a session that does not lead the project was captured: %+v", got)
	}
	if err := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB()).Enable(widgetKey, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	if got := captureOf(t, mac, "lead", "p-3", "and keep it fast"); !got.Captured || !got.Attach || len(got.Unattached) != 1 {
		t.Fatalf("the lead's words once the project is enabled: %+v", got)
	}
}

func TestEnablingTheLedgerOnOneHostEnablesItWhereverItIsKept(t *testing.T) {
	mac, owner, record := widgetOnTwoHosts(t)
	var assigned ledgerOwnerResponse
	mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, &assigned)
	if assigned.Owner != record.ID {
		t.Fatalf("owner = %+v", assigned)
	}
	macStore := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB())
	ownerStore := ledger.NewStore(owner.reg.WriteDB(), owner.reg.ReadDB())
	if err := macStore.Enable(widgetKey, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the owner holds the enable", 20*time.Second, func() bool {
		on, err := ownerStore.Enabled(widgetKey)
		return err == nil && on
	})
	if got := captureOf(t, owner, "remote-b", "p-1", "words to a worker"); !got.Captured || !got.Attach {
		t.Fatalf("a session on the owner host after the mac enabled the project: %+v", got)
	}
	if err := ownerStore.Disable(widgetKey); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the mac reads the owner's disable", 20*time.Second, func() bool {
		scope, err := macStore.Scope(widgetKey)
		return err == nil && scope.State == ledger.LedgerDisabled
	})
	if got := captureOf(t, mac, "lead", "p-2", "after the disable"); got.Captured {
		t.Fatalf("the lead was captured in a project disabled on its owner: %+v", got)
	}
	sameLedgerOnBothHosts(t, mac, owner)
}
