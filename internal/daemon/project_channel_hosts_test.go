package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/testkit"
)

type channelAnswer struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
	Recipients  []broadcast.Recipient `json:"recipients"`
	Hosts       []channelHostLeg      `json:"hosts"`
	Unreachable []string              `json:"unreachable"`
}

func channelSend(t *testing.T, node *federationNode, key string, body map[string]any) (int, channelAnswer) {
	t.Helper()
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
	request.AddCookie(browserCookie(t, node.reg))
	if key != "" {
		request.Header.Set(actionIdempotencyHeader, key)
	}
	response := httptest.NewRecorder()
	node.handler.ServeHTTP(response, request)
	var answer channelAnswer
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	return response.Code, answer
}

func viaWidget(messages []string) int {
	count := 0
	for _, message := range messages {
		if strings.Contains(message, " via #widget] ") {
			count++
		}
	}
	return count
}

func TestAProjectSendHoldsTheLegOfAHostItCannotReachAndNamesIt(t *testing.T) {
	local, remote, record := widgetOnTwoHosts(t)
	remote.stop()
	code, answer := channelSend(t, local, "", map[string]any{"action": "channel-send", "channel": "#widget", "message": "standup", "sourceSessionId": "local-a"})
	if code != http.StatusOK || len(answer.Recipients) != 1 || answer.Recipients[0].Coordinator != "@widget-lead" {
		t.Fatalf("status=%d answer=%+v, want this host's coordinator reached and the paired host held", code, answer)
	}
	if len(answer.Hosts) != 1 || answer.Hosts[0].State != channelLegHeld && answer.Hosts[0].State != channelLegUnknown || len(answer.Unreachable) != 1 || answer.Unreachable[0] != record.Name {
		t.Fatalf("hosts=%+v unreachable=%v, want the paired host named as held", answer.Hosts, answer.Unreachable)
	}
	// A stopped host refuses the dial (never sent) or resets a pooled connection after the write (outcome unknown).
	held, err := local.reg.HostHeldActions(record.ID)
	if err != nil || len(held) != 1 || !strings.HasSuffix(held[0].Key, ":"+record.ID) {
		t.Fatalf("held=%+v err=%v, want one leg held and keyed for the host", held, err)
	}
	if queued, err := local.reg.HostDeliveries(record.ID); err != nil || len(queued) != 0 {
		t.Fatalf("per-session holds=%+v err=%v, want the host to resolve its own members", queued, err)
	}
}

func TestAProjectSendWhoseHostDidNotAnswerIsDeliveredOnceWhenItDoes(t *testing.T) {
	local, remote, record := widgetOnTwoHosts(t)
	remote.gate.proxyDown.Store(true)
	code, answer := channelSend(t, local, "", map[string]any{"action": "channel-send", "channel": "#widget", "message": "standup", "author": "person"})
	if code != http.StatusOK || len(answer.Hosts) != 1 || answer.Hosts[0].State != channelLegUnknown || len(answer.Unreachable) != 1 {
		t.Fatalf("status=%d answer=%+v, want the leg held as unknown", code, answer)
	}
	if got := viaWidget(remote.fake.delivered()); got != 0 {
		t.Fatalf("the paired host delivered %d while its proxy refused", got)
	}
	remote.gate.proxyDown.Store(false)
	host, _ := local.daemon.pairedHost(record.ID)
	testkit.Eventually(t, "the held leg replayed", 10*time.Second, func() bool {
		local.daemon.drainHostDeliveries(context.Background(), host)
		held, err := local.reg.HostHeldActions(record.ID)
		return err == nil && len(held) == 0
	})
	local.daemon.drainHostDeliveries(context.Background(), host)
	if got := viaWidget(remote.fake.delivered()); got != 2 {
		t.Fatalf("the paired host delivered %d, want its coordinator and active member once each: %q", got, remote.fake.delivered())
	}
}

func TestAReplayOfALegTheHostAlreadyAppliedDeliversNothingTwice(t *testing.T) {
	local, remote, record := widgetOnTwoHosts(t)
	host, _ := local.daemon.pairedHost(record.ID)
	body, _ := json.Marshal(map[string]any{"action": "channel-deliver", "projectId": "repo:github.com/example/widget", "message": "[once]"})
	key := "channel-applied:" + record.ID
	response, err := host.client.Do(context.Background(), http.MethodPost, "/api/v1/actions", nil, body, http.Header{actionIdempotencyHeader: []string{key}})
	if err != nil {
		t.Fatal(err)
	}
	if answer, err := readHostAnswer(host, response); err != nil || answer.status != http.StatusOK {
		t.Fatalf("first attempt status=%d err=%v", answer.status, err)
	}
	if _, err := local.reg.HoldHostAction(record.ID, key, "#widget", body, true); err != nil {
		t.Fatal(err)
	}
	local.daemon.drainHostDeliveries(context.Background(), host)
	if held, err := local.reg.HostHeldActions(record.ID); err != nil || len(held) != 0 {
		t.Fatalf("held=%+v err=%v, want the replay settled from the host's stored answer", held, err)
	}
	if got := remote.fake.delivered(); len(got) != 2 {
		t.Fatalf("delivered=%q, want the first attempt's two deliveries and nothing from the replay", got)
	}
}

func TestARetriedProjectSendDeliversOncePerHostAndNeverEchoes(t *testing.T) {
	local, remote, _ := widgetOnTwoHosts(t)
	for range 2 {
		if code, answer := channelSend(t, local, "standup-1", map[string]any{"action": "channel-send", "channel": "#widget", "message": "standup", "sourceSessionId": "local-a"}); code != http.StatusOK || len(answer.Recipients) != 3 {
			t.Fatalf("status=%d answer=%+v", code, answer)
		}
	}
	if got := viaWidget(remote.fake.delivered()); got != 2 {
		t.Fatalf("the paired host delivered %d, want 2 for one send: %q", got, remote.fake.delivered())
	}
	if got := viaWidget(local.fake.delivered()); got != 1 {
		t.Fatalf("this host delivered %d, want its coordinator once and nothing echoed back: %q", got, local.fake.delivered())
	}
}

func TestAnAgentReachesOnlyItsOwnProjectOnAPairedHost(t *testing.T) {
	local, remote, record := widgetOnTwoHosts(t)
	moveSession(t, local, "gadget-a", widgetRepo(t, "https://github.com/example/gadget.git"), time.Now())
	local.daemon.discoverCatalog(context.Background())

	code, answer := channelSend(t, local, "", map[string]any{"action": "channel-send", "channel": "#widget", "message": "from another project", "sourceSessionId": "gadget-a"})
	if code != http.StatusConflict || len(answer.Hosts) != 1 || answer.Hosts[0].State != channelLegRefused || answer.Hosts[0].Rule != "different_project" {
		t.Fatalf("status=%d hosts=%+v, want the paired host to refuse a sender from another project", code, answer.Hosts)
	}
	if got := viaWidget(remote.fake.delivered()); got != 0 {
		t.Fatalf("the paired host delivered %d from another project's agent", got)
	}

	host, _ := local.daemon.pairedHost(record.ID)
	unnamed, _ := json.Marshal(map[string]any{"action": "channel-deliver", "projectId": "repo:github.com/example/widget", "message": "no project named", "sourceSessionId": hosts.Qualify(local.daemon.hostIdentity().ID, "gadget-a")})
	response, err := host.client.Do(context.Background(), http.MethodPost, "/api/v1/actions", nil, unnamed, http.Header{actionIdempotencyHeader: []string{"unnamed-project"}})
	if err != nil {
		t.Fatal(err)
	}
	refusal, err := readHostAnswer(host, response)
	var body channelDeliverAnswer
	if err != nil || json.Unmarshal(refusal.payload, &body) != nil || refusal.status != http.StatusForbidden || body.Detail.Rule != "project_unconfirmed" {
		t.Fatalf("status=%d body=%s err=%v, want project_unconfirmed", refusal.status, refusal.payload, err)
	}

	if code, answer := channelSend(t, local, "", map[string]any{"action": "channel-send", "channel": "#widget", "message": "from the person", "author": "person"}); code != http.StatusOK || len(answer.Hosts) != 1 || answer.Hosts[0].State != channelLegReached {
		t.Fatalf("status=%d hosts=%+v, want the person's post accepted", code, answer.Hosts)
	}
	if got := viaWidget(remote.fake.delivered()); got != 2 {
		t.Fatalf("the paired host delivered %d of the person's post, want 2", got)
	}
}

func TestAnUnansweredHeldSendIsGivenUpAfterThePatienceWindow(t *testing.T) {
	local, remote, record := widgetOnTwoHostsWith(t, func(f *federation) { f.patience = 0 })
	remote.gate.proxyDown.Store(true)
	if code, answer := channelSend(t, local, "", map[string]any{"action": "channel-send", "channel": "#widget", "message": "standup", "author": "person"}); code != http.StatusOK || answer.Hosts[0].State != channelLegUnknown {
		t.Fatalf("status=%d answer=%+v", code, answer)
	}
	direct := local.request(t, http.MethodPost, "/api/v1/actions", map[string]string{"action": "send", "sessionId": hosts.Qualify(record.ID, "remote-c"), "message": "direct"})
	if direct.Code != http.StatusOK || !strings.Contains(direct.Body.String(), `"evidence":"unknown"`) {
		t.Fatalf("direct send status=%d body=%s", direct.Code, direct.Body)
	}
	host, _ := local.daemon.pairedHost(record.ID)
	local.daemon.drainHostDeliveries(context.Background(), host)
	if held, err := local.reg.HostHeldActions(record.ID); err != nil || len(held) != 0 {
		t.Fatalf("held=%+v err=%v, want the unanswered leg given up", held, err)
	}
	history, err := local.reg.ListHistory(20, "")
	failed := false
	for _, entry := range history {
		failed = failed || entry.Kind == "channel" && entry.Result == "failed" && strings.Contains(entry.Error, "has not confirmed")
	}
	if err != nil || !failed {
		t.Fatalf("history=%+v err=%v, want the given-up leg recorded", history, err)
	}
	queue, err := local.reg.ListQueue(true, nil)
	given := 0
	for _, item := range queue {
		if item.Message == "direct" && item.Status != "inflight" {
			given++
		}
	}
	if err != nil || given != 1 {
		t.Fatalf("queue=%+v err=%v, want the unanswered direct send given up too", queue, err)
	}
}

func TestASendRoutedToAPairedHostKeepsTheSendersProjectReach(t *testing.T) {
	local, remote, _ := widgetOnTwoHosts(t)
	moveSession(t, local, "gadget-a", widgetRepo(t, "https://github.com/example/gadget.git"), time.Now())
	local.daemon.discoverCatalog(context.Background())
	response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "host": "vps", "channel": "#repo:github.com/example/widget", "message": "routed", "sourceSessionId": "gadget-a"})
	var refusal channelDeliverAnswer
	if response.Code != http.StatusForbidden || json.Unmarshal(response.Body.Bytes(), &refusal) != nil || refusal.Detail.Rule != "different_project" {
		t.Fatalf("status=%d body=%s, want the paired host to refuse another project's sender", response.Code, response.Body)
	}
	if got := viaWidget(remote.fake.delivered()); got != 0 {
		t.Fatalf("the paired host delivered %d", got)
	}
	same := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-send", "host": "vps", "channel": "#repo:github.com/example/widget", "message": "routed", "sourceSessionId": "local-a"})
	if same.Code != http.StatusOK || viaWidget(remote.fake.delivered()) != 2 {
		t.Fatalf("status=%d body=%s delivered=%q, want the project's own session to reach it", same.Code, same.Body, remote.fake.delivered())
	}
}

func TestOnlyAPairedHostSendsChannelDeliver(t *testing.T) {
	local, _, _ := widgetOnTwoHosts(t)
	response := local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "channel-deliver", "projectId": "repo:github.com/example/widget", "message": "direct"})
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "host_only") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}
