package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/testkit"
)

func listedHosts(t *testing.T, node *federationNode) map[string]hostView {
	t.Helper()
	response := node.request(t, http.MethodGet, "/api/v1/hosts", nil)
	var listing struct {
		Hosts []hostView `json:"hosts"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &listing) != nil {
		t.Fatalf("hosts status=%d body=%s", response.Code, response.Body.String())
	}
	views := map[string]hostView{}
	for _, view := range listing.Hosts {
		views[view.Name] = view
	}
	return views
}

func tokensFor(t *testing.T, node *federationNode, name string) (active, revoked []registry.Device) {
	t.Helper()
	devices, err := node.reg.ListDevices(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range devices {
		if device.Name != name {
			continue
		}
		if device.RevokedAt == "" {
			active = append(active, device)
		} else {
			revoked = append(revoked, device)
		}
	}
	return active, revoked
}

func relayLeg(t *testing.T, from *federationNode, target, source, message string) int64 {
	t.Helper()
	id, err := from.reg.QueueHostSend(registry.HostSend{SessionID: target, Message: message, Key: "relay:" + message, SourceSessionID: source, RelayHops: 1})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func awaitRelayLeg(t *testing.T, node *federationNode, target, message string) {
	t.Helper()
	testkit.Eventually(t, "relay leg "+message+" queued on "+target, 10*time.Second, func() bool {
		claimed, err := node.reg.ClaimNextMessage(target, time.Now())
		if err != nil || claimed == nil {
			return false
		}
		if ackErr := node.reg.AckMessage(claimed.ID); ackErr != nil {
			t.Fatal(ackErr)
		}
		return claimed.Message == message && claimed.RelayHops == 1
	})
}

func TestAnOldStylePairingIsNamedAndPairingAgainRepairsItInPlace(t *testing.T) {
	for _, test := range []struct {
		name   string
		scopes []string
		bound  bool
	}{
		{"without the host scope", []string{"read", "control"}, true},
		{"not bound to its host", []string{"read", "control", registry.HostScope}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			laptop := newFederationNode(t, "laptop-a")
			vps := newFederationNode(t, "vps-a")
			laptop.serve(t, nil)
			vps.serve(t, nil)
			roots := testRoots(laptop.server)
			laptop.start(t, roots)
			vps.start(t, roots)
			vpsRecord := laptop.pair(t, vps, "vps")
			laptopIdentity, _ := laptop.reg.HostIdentity()
			vpsIdentity, _ := vps.reg.HostIdentity()

			pairing, err := laptop.reg.CreateDevicePairing("Agenthail host", test.scopes, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			hostID := ""
			if test.bound {
				hostID = vpsIdentity.ID
			}
			_, oldToken, err := laptop.reg.CompleteDevicePairing(pairing.Secret, vpsIdentity.Name, hostID)
			if err != nil {
				t.Fatal(err)
			}
			laptopRecord, err := vps.reg.AddHost(registry.PairedHost{ID: laptopIdentity.ID, Name: "laptop", Endpoint: laptop.endpoint(), Address: laptop.address, Token: oldToken})
			if err != nil {
				t.Fatal(err)
			}
			if err := vps.daemon.attachHost(laptopRecord); err != nil {
				t.Fatal(err)
			}
			testkit.Eventually(t, "both mirrors", 10*time.Second, func() bool {
				return len(sessionsOf(laptop.snapshot(t), vpsRecord.ID)) == 1 && len(sessionsOf(vps.snapshot(t), laptopRecord.ID)) == 1
			})

			fix := hosts.Unproven("laptop")
			testkit.Eventually(t, "the host list names the old pairing", 10*time.Second, func() bool {
				return listedHosts(t, vps)["laptop"].Problem == fix
			})
			if problem := listedHosts(t, laptop)["vps"].Problem; problem != "" {
				t.Fatalf("the laptop's own pairing reports %q", problem)
			}

			refused := relayLeg(t, vps, "laptop-a", "vps-a", "before")
			var card registry.Ask
			testkit.Eventually(t, "the refused relay asks for a pairing", 10*time.Second, func() bool {
				asks, _ := vps.reg.OpenAsks()
				index := slices.IndexFunc(asks, func(ask registry.Ask) bool {
					return ask.Ref == fmt.Sprintf("queue:%d:dead", refused)
				})
				if index >= 0 {
					card = asks[index]
				}
				return index >= 0
			})
			if !strings.HasPrefix(card.Questions[0].Question, "Host laptop doesn't know this host as a paired host. On laptop run 'agenthail hosts invite', then here run 'agenthail hosts add <pairing-link>'") {
				t.Fatalf("ask=%+v", card)
			}
			relayLeg(t, laptop, "vps-a", "laptop-a", "outbound")
			awaitRelayLeg(t, vps, "vps-a", "outbound")

			if err := vps.reg.SetAlias("lap", "laptop-a"); err != nil {
				t.Fatal(err)
			}
			route := vps.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "relay-add", "fromId": "vps-a", "toId": hosts.Qualify(laptopIdentity.ID, "laptop-a")})
			if route.Code != http.StatusOK {
				t.Fatalf("relay-add status=%d body=%s", route.Code, route.Body.String())
			}
			routesBefore, _ := vps.reg.ListRoutes()
			store := ledger.NewStore(vps.reg.WriteDB(), vps.reg.ReadDB())
			if err := store.AssignOwner("repo:example", laptopIdentity.ID); err != nil {
				t.Fatal(err)
			}
			before, _ := vps.reg.Host(laptopIdentity.ID)

			response := vps.request(t, http.MethodPost, "/api/v1/hosts", map[string]string{"pairingUrl": laptop.invite(t), "address": laptop.address})
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"repaired":true`) || strings.Contains(response.Body.String(), "ahd_") {
				t.Fatalf("pair again status=%d body=%s", response.Code, response.Body.String())
			}

			after, err := vps.reg.Host(laptopIdentity.ID)
			if err != nil || after.Name != before.Name || after.CreatedAt != before.CreatedAt || after.CatalogEpoch != before.CatalogEpoch {
				t.Fatalf("host record before=%+v after=%+v err=%v", before, after, err)
			}
			if listed, _ := vps.reg.ListHosts(); len(listed) != 1 {
				t.Fatalf("pairing again duplicated the host: %+v", listed)
			}
			active, revoked := tokensFor(t, laptop, vpsIdentity.Name)
			if len(active) != 1 || !active[0].IsHost() || active[0].HostID != vpsIdentity.ID || len(revoked) != 1 {
				t.Fatalf("laptop tokens for vps active=%+v revoked=%+v", active, revoked)
			}
			testkit.Eventually(t, "the host list clears", 10*time.Second, func() bool {
				return listedHosts(t, vps)["laptop"].Problem == ""
			})
			if rows := sessionsOf(vps.snapshot(t), laptopIdentity.ID); len(rows) != 1 || rows[0].ID != hosts.Qualify(laptopIdentity.ID, "laptop-a") {
				t.Fatalf("mirrored rows after pairing again=%+v", rows)
			}
			if alias, _ := vps.reg.ReverseAlias("laptop-a"); alias != "lap" {
				t.Fatalf("alias after pairing again=%q", alias)
			}
			if routes, _ := vps.reg.ListRoutes(); len(routes) != 1 || len(routesBefore) != 1 || routes[0].ID != routesBefore[0].ID || routes[0].ToSession != "laptop-a" {
				t.Fatalf("routes before=%+v after=%+v", routesBefore, routes)
			}
			if owner, _, _ := store.Owner("repo:example"); owner != laptopIdentity.ID {
				t.Fatalf("ledger owner after pairing again=%q", owner)
			}

			retry := vps.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": card.SessionID, "requestId": card.ID, "decision": map[string]any{"choice": "retry"}})
			if retry.Code != http.StatusOK {
				t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
			}
			awaitRelayLeg(t, laptop, "laptop-a", "before")
			relayLeg(t, vps, "laptop-a", "vps-a", "after")
			awaitRelayLeg(t, laptop, "laptop-a", "after")
			relayLeg(t, laptop, "vps-a", "laptop-a", "outbound again")
			awaitRelayLeg(t, vps, "vps-a", "outbound again")
		})
	}
}

func TestPairingAgainRefusesAnotherName(t *testing.T) {
	local, remote, record := pairedFederation(t)
	response := local.request(t, http.MethodPost, "/api/v1/hosts", map[string]string{"pairingUrl": remote.invite(t), "address": remote.address, "name": "elsewhere"})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "already paired as "+record.Name) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	localIdentity, _ := local.reg.HostIdentity()
	if active, _ := tokensFor(t, remote, localIdentity.Name); len(active) != 1 {
		t.Fatalf("the refused pairing left %d tokens on the host", len(active))
	}
}
