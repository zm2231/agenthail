package daemon

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type leadAcrossHosts struct {
	laptop, vps     *federationNode
	worker, workerQ string
}

// leadOnTheOtherHost puts laptop-a in the widget project with no recorded
// spawner, and the project's lead, vps-a, on the paired host vps, with vps-b
// beside it.
func leadOnTheOtherHost(t *testing.T) leadAcrossHosts {
	t.Helper()
	laptop, vps, vpsRecord, laptopRecord := pairedBothWays(t)
	widgetHere := widgetRepo(t, "https://github.com/example/widget.git")
	widgetThere := widgetRepo(t, "https://github.com/example/widget.git")
	moveSession(t, laptop, "laptop-a", widgetHere, time.Now())
	moveSession(t, vps, "vps-a", widgetThere, time.Now())
	moveSession(t, vps, "vps-b", widgetThere, time.Now())
	setLead(t, vps, widgetProject, widgetThere, "widget-lead", "vps-a", registry.CoordinatorChange{})
	vps.daemon.discoverCatalog(context.Background())
	laptop.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the lead mirrored here and the worker mirrored there", 10*time.Second, func() bool {
		leads, _ := laptop.reg.ProjectCoordinators(widgetProject)
		mirrored, _ := vps.reg.MatchHostSessions(laptopRecord.ID, "laptop-a")
		return len(leads) == 1 && leads[0].HostID == vpsRecord.ID && laptop.daemon.coordinator().LeadSession(leads[0]) == "vps-a" && len(mirrored) == 1
	})
	if _, found, err := laptop.reg.SessionOrigin("laptop-a"); err != nil || found {
		t.Fatalf("found=%v err=%v, want the worker to have no recorded spawner", found, err)
	}
	return leadAcrossHosts{laptop: laptop, vps: vps, worker: "laptop-a", workerQ: hosts.Qualify(mustHostID(t, laptop), "laptop-a")}
}

func (l leadAcrossHosts) prompt(t *testing.T, ref string) registry.Ask {
	t.Helper()
	l.laptop.fake.mu.Lock()
	l.laptop.fake.open = map[string][]surface.Request{l.worker: {{ID: ref, Kind: surface.WaitingApproval, Tool: "Bash"}}}
	l.laptop.fake.mu.Unlock()
	worker, err := l.laptop.reg.Session(l.worker)
	if err != nil {
		t.Fatal(err)
	}
	l.laptop.daemon.syncWaits(*worker, &surface.Waiting{Kind: surface.WaitingApproval, ID: ref, Answerable: true})
	l.laptop.daemon.background.wait()
	open, err := l.laptop.reg.OpenPermissionAsks(l.worker)
	if err != nil || len(open) != 1 {
		t.Fatalf("open=%+v err=%v", open, err)
	}
	return open[0]
}

func (l leadAcrossHosts) answerAs(t *testing.T, sessionID, askID, key string) (map[string]any, bool) {
	t.Helper()
	session, err := l.vps.reg.Session(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	client := mcpSession(t, mcpServer(t, l.vps.daemon), issueMCPToken(t, l.vps.daemon, *session))
	return callMCP(t, client, "agenthail_answer", map[string]any{"requestId": askID, "sessionId": l.workerQ, "decision": "allow", "clientRequestId": key})
}

func TestAPromptFromASessionThePersonStartedGoesToTheLeadOnAPairedHostFirst(t *testing.T) {
	l := leadOnTheOtherHost(t)
	ask := l.prompt(t, "prompt-1")
	if ask.RoutedTo != "vps-a" {
		t.Fatalf("routed to %q, want the project's lead on the paired host", ask.RoutedTo)
	}
	testkit.Eventually(t, "the lead told how to answer from its own host", 10*time.Second, func() bool {
		return slices.ContainsFunc(l.vps.fake.delivered(), func(got string) bool {
			return strings.Contains(got, ask.ID) && strings.Contains(got, `"sessionId":"`+l.workerQ+`"`)
		})
	})
	escalated := l.laptop.daemon.escalateAsk(ask, "")
	if escalated.RoutedTo != registry.AskToPerson {
		t.Fatalf("after the window the ask went to %q, want the person", escalated.RoutedTo)
	}
}

func TestALeadOnAPairedHostAnswersItsWorkersPromptAsItself(t *testing.T) {
	l := leadOnTheOtherHost(t)
	ask := l.prompt(t, "prompt-1")
	if out, refused := l.answerAs(t, "vps-b", ask.ID, "bystander"); !refused {
		t.Fatalf("a session the prompt is not routed to answered it: %v", out)
	}
	if out, refused := l.answerAs(t, "vps-a", ask.ID, "lead"); refused {
		t.Fatalf("the lead's answer from its own host: %v", out)
	}
	l.laptop.fake.mu.Lock()
	_, answered := l.laptop.fake.answered[l.worker+"/prompt-1"]
	l.laptop.fake.mu.Unlock()
	if !answered {
		t.Fatal("the worker's prompt was not answered")
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
