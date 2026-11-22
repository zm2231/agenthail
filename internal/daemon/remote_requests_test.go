package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

var requestsCall = regexp.MustCompile(`agenthail_requests (\{[^}\n]*\})`)

func TestALeadReadsItsWorkersPromptOnAPairedHostFromTheNotice(t *testing.T) {
	l := leadOnTheOtherHost(t)
	l.laptop.fake.mu.Lock()
	l.laptop.fake.open = map[string][]surface.Request{l.worker: {{ID: "prompt-1", Kind: surface.WaitingApproval, Tool: "Bash", Input: json.RawMessage(`{"command":"make deploy"}`)}}}
	l.laptop.fake.mu.Unlock()
	waiting := &surface.Waiting{Kind: surface.WaitingApproval, ID: "prompt-1", Answerable: true}
	busy := l.laptop.fake.sessions[l.worker]
	busy.Status, busy.WaitingOn = surface.StatusBusy, waiting
	l.laptop.fake.sessions[l.worker] = busy
	l.laptop.fake.observations[l.worker] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t1", WaitingOn: waiting}
	worker, err := l.laptop.reg.Session(l.worker)
	if err != nil {
		t.Fatal(err)
	}
	l.laptop.daemon.syncWaits(*worker, waiting)
	l.laptop.daemon.background.wait()
	var call map[string]any
	testkit.Eventually(t, "the lead told how to read the prompt", 10*time.Second, func() bool {
		notices := l.vps.fake.delivered()
		index := slices.IndexFunc(notices, func(got string) bool { return requestsCall.MatchString(got) })
		return index >= 0 && json.Unmarshal([]byte(requestsCall.FindStringSubmatch(notices[index])[1]), &call) == nil
	})
	lead, err := l.vps.reg.Session("vps-a")
	if err != nil {
		t.Fatal(err)
	}
	client := mcpSession(t, mcpServer(t, l.vps.daemon), issueMCPToken(t, l.vps.daemon, *lead))
	one, refused := callMCP(t, client, "agenthail_requests", call)
	request, _ := one["request"].(map[string]any)
	if refused || request["detail"] != "make deploy" || one["sessionId"] != l.workerQ {
		t.Fatalf("the notice's own call %v answered %v", call, one)
	}
	listed, refused := callMCP(t, client, "agenthail_requests", map[string]any{"sessionId": call["sessionId"]})
	if requests, _ := listed["requests"].([]any); refused || len(requests) != 1 {
		t.Fatalf("the worker's requests by the notice's sessionId: %v", listed)
	}
	var row map[string]any
	testkit.Eventually(t, "the lead's waiting list to show the worker on the paired host", 10*time.Second, func() bool {
		l.laptop.daemon.discoverCatalog(t.Context())
		l.vps.daemon.discoverCatalog(t.Context())
		waiting, _ := callMCP(t, client, "agenthail_requests", nil)
		rows, _ := waiting["waiting"].([]any)
		for _, raw := range rows {
			if candidate := raw.(map[string]any); candidate["sessionId"] == l.workerQ {
				row = candidate
				return true
			}
		}
		return false
	})
	if row["detail"] != "make deploy" || row["requestId"] != call["requestId"] || row["tool"] != "Bash" {
		t.Fatalf("the waiting row for the worker on the paired host: %v, want its request %v with what it runs", row, call["requestId"])
	}
	moveSession(t, l.vps, "vps-later", widgetRepo(t, "https://github.com/example/widget.git"), time.Now())
	l.vps.daemon.discoverCatalog(t.Context())
	later, err := l.vps.reg.Session("vps-later")
	if err != nil {
		t.Fatal(err)
	}
	l.vps.daemon.syncWaits(*later, &surface.Waiting{Kind: surface.WaitingApproval, ID: "prompt-2", Answerable: true})
	l.vps.daemon.background.wait()
	both, _ := callMCP(t, client, "agenthail_requests", nil)
	var order []any
	for _, raw := range both["waiting"].([]any) {
		order = append(order, raw.(map[string]any)["sessionId"])
	}
	if !slices.Equal(order, []any{l.workerQ, "vps-later"}) {
		t.Fatalf("waiting list order %v, want the worker mirrored first before the session started after it", order)
	}

	moveSession(t, l.laptop, "laptop-gadget", widgetRepo(t, "https://github.com/example/gadget.git"), time.Now())
	elsewhere := hosts.Qualify(mustHostID(t, l.laptop), "laptop-gadget")
	if out, refused := callMCP(t, client, "agenthail_requests", map[string]any{"sessionId": elsewhere}); !refused || out["code"] != "out_of_scope" {
		t.Fatalf("a session on the paired host outside the lead's project was read: %v", out)
	}
	if out, refused := callMCP(t, client, "agenthail_requests", map[string]any{"sessionId": "laptop"}); !refused || !strings.Contains(fmt.Sprint(out["message"]), "ambiguous") {
		t.Fatalf("a name two sessions on the paired host match: %v, want it refused as ambiguous", out)
	}

	settled := l.laptop.fake.sessions[l.worker]
	settled.Status, settled.WaitingOn = surface.StatusIdle, nil
	l.laptop.fake.sessions[l.worker] = settled
	if err := l.laptop.reg.ArchiveSession(l.worker, time.Now()); err != nil {
		t.Fatal(err)
	}
	var archived map[string]any
	testkit.Eventually(t, "the archived worker refused", 10*time.Second, func() bool {
		l.laptop.daemon.discoverCatalog(t.Context())
		l.vps.daemon.discoverCatalog(t.Context())
		archived, _ = callMCP(t, client, "agenthail_requests", map[string]any{"sessionId": l.workerQ})
		detail, _ := archived["detail"].(map[string]any)
		return archived["code"] == "out_of_scope" && detail["rule"] == "archived"
	})
}

func TestALeadAnswersItsWorkersPromptOnAPairedHostFromTheCLI(t *testing.T) {
	l := leadOnTheOtherHost(t)
	ask := l.prompt(t, "prompt-1")
	const local = "127.0.0.1:50000"
	if answer := askScope(t, l.vps.daemon, daemonapi.ScopeRequest{SessionID: "vps-a", Action: "answer", Target: l.workerQ, Mode: "allow"}, local); answer.status != http.StatusOK || answer.reply.TargetID != l.workerQ {
		t.Fatalf("the lead's answer scope: status=%d reply=%+v body=%s", answer.status, answer.reply, answer.body)
	}
	if answer := askScope(t, l.vps.daemon, daemonapi.ScopeRequest{SessionID: "vps-a", Action: "answer", Target: l.workerQ, Mode: "always"}, local); answer.code != "exceeds_ceiling" {
		t.Fatalf("always allow from the lead: status=%d code=%q, want exceeds_ceiling", answer.status, answer.code)
	}
	respond := func(source string) *httptest.ResponseRecorder {
		return postAction(t, l.vps.handler, map[string]any{"action": "respond", "sessionId": l.workerQ, "requestId": ask.ID, "decision": map[string]any{"choices": []string{"allow"}}, "sourceSessionId": source})
	}
	if response := respond("vps-b"); response.Code != http.StatusForbidden {
		t.Fatalf("a session the prompt is not routed to answered it: %d %s", response.Code, response.Body)
	}
	if response := respond("vps-a"); response.Code != http.StatusOK {
		t.Fatalf("the lead's answer: %d %s", response.Code, response.Body)
	}
	l.laptop.daemon.background.wait()
	history, err := l.laptop.reg.ListHistory(20, l.worker)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(history, func(entry registry.HistoryEntry) bool {
		return entry.Kind == "request-allowed" && strings.Contains(entry.Result, "vps-a") && !strings.Contains(entry.Result, "person")
	}) {
		t.Fatalf("history=%+v, want the answer recorded as the lead's", history)
	}
}
