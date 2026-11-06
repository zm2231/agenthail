package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/testkit"
)

const renamedWidgetKey = "repo:github.com/example/renamed-widget"

func TestARemoteChangeCarriesASharedLedgerOnBothHosts(t *testing.T) {
	for name, ownerMoves := range map[string]bool{"the owner sees it first": true, "the replica sees it first": false} {
		t.Run(name, func(t *testing.T) {
			mac, owner, _ := pairedFederation(t)
			macRepo := widgetRepo(t, "https://github.com/example/widget.git")
			ownerRepo := widgetRepo(t, "https://github.com/example/widget.git")
			moveSession(t, mac, "local-a", macRepo, time.Now())
			moveSession(t, owner, "remote-a", ownerRepo, time.Now())
			owner.daemon.discoverCatalog(context.Background())
			mac.daemon.discoverCatalog(context.Background())
			if err := ledger.NewStore(mac.reg.WriteDB(), mac.reg.ReadDB()).Enable(widgetKey, ledger.AttachEachPrompt); err != nil {
				t.Fatal(err)
			}
			capturePrompt(t, mac, "local-a", "p-1", "before the rename")
			mustLedger(t, mac, "/api/v1/ledger/owner", ledgerOwnerRequest{ProjectKey: widgetKey, Host: "vps"}, nil)
			sameLedgerOnBothHosts(t, widgetKey, mac, owner)

			moved, lagging := owner, mac
			movedRepo := ownerRepo
			if !ownerMoves {
				moved, lagging, movedRepo = mac, owner, macRepo
			}
			gitRun(t, movedRepo, "remote", "set-url", "origin", "https://github.com/example/renamed-widget.git")
			moved.daemon.discoverCatalog(context.Background())
			if ownerMoves {
				testkit.Eventually(t, "the replica takes the owner's move", 20*time.Second, func() bool {
					held, err := lagging.reg.HoldsLedger(renamedWidgetKey)
					return err == nil && held
				})
			}

			capturePrompt(t, mac, "local-a", "p-2", "after the rename")
			sameLedgerOnBothHosts(t, renamedWidgetKey, mac, owner)
			for _, node := range []*federationNode{mac, owner} {
				if held, err := node.reg.HoldsLedger(widgetKey); err != nil || held {
					t.Fatalf("a host still keeps history under the old key: %t %v", held, err)
				}
				if texts := utteranceTexts(t, node.daemon.ledgerStore(), renamedWidgetKey); len(texts) != 2 {
					t.Fatalf("a host's ledger holds %q, want both messages", texts)
				}
			}
		})
	}
}
