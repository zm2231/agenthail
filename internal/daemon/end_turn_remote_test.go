package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAnAskOnAPairedHostsSessionShowsAndListsHere(t *testing.T) {
	local, remote, record := pairedFederation(t)
	end := registry.TurnEnd{SessionID: "remote-a", Needs: "you", Headline: "Ready to ship", Source: registry.TurnSourceTool,
		Asks: []registry.TurnAsk{{Question: "Deploy it now?", Options: []registry.TurnOption{{Label: "Deploy"}, {Label: "Wait"}}}}}
	if _, err := remote.reg.EndTurn(end, "turn-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	remote.daemon.discoverCatalog(context.Background())
	qualified := hosts.Qualify(record.ID, "remote-a")

	testkit.Eventually(t, "the ask mirrored into this host's session list", 10*time.Second, func() bool {
		for _, row := range sessionsOf(local.snapshot(t), record.ID) {
			if row.ID == qualified {
				return row.EndTurn != nil && row.EndTurn.Headline == "Ready to ship"
			}
		}
		return false
	})
	row := viewRow(t, local.daemon, qualified)
	if row.EndTurn == nil || len(row.EndTurn.Asks) != 1 || row.Attention == nil || row.Attention.Kind != AttentionAsk {
		t.Fatalf("session view row endTurn=%+v attention=%+v", row.EndTurn, row.Attention)
	}

	response := local.request(t, http.MethodGet, "/api/v1/requests?sessionId="+url.QueryEscape(qualified), nil)
	var body struct {
		Requests []openRequestView `json:"requests"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("requests status=%d body=%s", response.Code, response.Body.String())
	}
	if len(body.Requests) != 1 || body.Requests[0].Kind != surface.WaitingAsk || len(body.Requests[0].Questions) != 1 {
		t.Fatalf("requests through the host = %+v", body.Requests)
	}
}
