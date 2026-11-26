package daemon

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

var (
	noticeSessionID = regexp.MustCompile(`"sessionId":"([^"]+)"`)
	noticeLast      = regexp.MustCompile(`agenthail last ([^\s,]+)`)
)

var parserAsk = map[string]any{"title": "Which parser", "default": 1, "defaultAfterMinutes": 30, "questions": []any{map[string]any{"question": "Hand-written or generated?", "recommended": 1, "options": []any{map[string]any{"label": "Hand-written"}, map[string]any{"label": "Generated"}}}}}

func listedAsk(t *testing.T, session *mcp.ClientSession, sessionID, askID string) map[string]any {
	t.Helper()
	out, refused := callMCP(t, session, "agenthail_requests", map[string]any{"sessionId": sessionID})
	if refused {
		t.Fatalf("requests of %s: %v", sessionID, out)
	}
	requests, _ := out["requests"].([]any)
	for _, raw := range requests {
		if request := raw.(map[string]any); request["id"] == askID {
			return request
		}
	}
	return nil
}

func waitingAsk(t *testing.T, session *mcp.ClientSession, askID string) map[string]any {
	t.Helper()
	out, refused := callMCP(t, session, "agenthail_requests", nil)
	if refused {
		t.Fatalf("waiting list: %v", out)
	}
	rows, _ := out["waiting"].([]any)
	for _, raw := range rows {
		if row := raw.(map[string]any); row["requestId"] == askID {
			return row
		}
	}
	return nil
}

func parserAskShown(t *testing.T, what string, holder map[string]any) {
	t.Helper()
	ask, _ := holder["ask"].(map[string]any)
	questions, _ := ask["questions"].([]any)
	if ask["title"] != "Which parser" || len(questions) != 1 {
		t.Fatalf("%s: %v, want the ask's title and question", what, holder)
	}
	question := questions[0].(map[string]any)
	if options, _ := question["options"].([]any); len(options) != 2 || question["recommended"] == nil || question["recommended"] != question["default"] || ask["defaultAt"] == nil {
		t.Fatalf("%s: %v, want its two options, the recommended one as its default, and when the default applies", what, ask)
	}
}

func TestAnAskRoutedToASessionIsAmongTheRequestsItReads(t *testing.T) {
	f := newAskFixture(t)
	askID := f.raise(t, f.worker, parserAsk)["askId"].(string)
	if routed := f.ask(t, askID).RoutedTo; routed != f.from.ID {
		t.Fatalf("the worker's ask went to %q, want its spawner", routed)
	}
	parserAskShown(t, "the worker's requests", listedAsk(t, f.person, f.to.ID, askID))
	one, refused := callMCP(t, f.person, "agenthail_requests", map[string]any{"sessionId": f.to.ID, "requestId": askID})
	if request, _ := one["request"].(map[string]any); refused || request == nil {
		t.Fatalf("the ask by its id: %v", one)
	} else {
		parserAskShown(t, "the ask by its id", request)
	}
	row := waitingAsk(t, f.person, askID)
	if row == nil || row["sessionId"] != f.to.ID {
		t.Fatalf("the waiting list's row for the ask: %v", row)
	}
	parserAskShown(t, "the waiting row", row)
	if open, err := f.d.openRequests(t.Context(), &f.to); err == nil && slices.ContainsFunc(open.requests, func(request openRequestView) bool { return request.ID == askID }) {
		t.Fatal("the person's requests list an ask routed to an agent")
	}
	if out, refused := callMCP(t, f.person, "agenthail_answer", map[string]any{"requestId": askID, "sessionId": f.to.ID, "answers": map[string]any{"Hand-written or generated?": "Generated"}}); refused {
		t.Fatalf("answer: %v", out)
	}
	if row := waitingAsk(t, f.person, askID); row != nil {
		t.Fatalf("an answered ask is still listed: %v", row)
	}
}

func TestTheSessionAWorkersAskNoticeNamesWorksInEveryToolOnItsStartersHost(t *testing.T) {
	f := startAcrossHosts(t)
	askID := f.workerAsk(t)
	var notice string
	testkit.Eventually(t, "the ask told to the starter on the laptop", 15*time.Second, func() bool {
		index := slices.IndexFunc(f.laptop.fake.delivered(), func(got string) bool { return strings.Contains(got, "asks: Which parser") })
		if index >= 0 {
			notice = f.laptop.fake.delivered()[index]
		}
		return index >= 0
	})
	printed := noticeSessionID.FindStringSubmatch(notice)[1]
	if printed == f.created {
		t.Fatalf("the notice names %s; this test needs the alias form it prints for an aliased worker", printed)
	}
	if out, refused := callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": printed}); refused || out["sessionId"] != f.created {
		t.Fatalf("read by the notice's %s: refused=%v out=%v", printed, refused, out)
	}
	parserAskShown(t, "the worker's requests by the notice's name", listedAsk(t, f.starter, printed, askID))
	one, refused := callMCP(t, f.starter, "agenthail_requests", map[string]any{"sessionId": printed, "requestId": askID})
	if request, _ := one["request"].(map[string]any); refused || request == nil {
		t.Fatalf("the ask by the notice's name and id: %v", one)
	} else {
		parserAskShown(t, "the ask by the notice's name and id", request)
	}
	out, refused := callMCP(t, f.bystander, "agenthail_requests", map[string]any{"sessionId": printed})
	refusedAs(t, "a session that did not start the worker reading its requests", out, refused, "out_of_scope")
	f.vps.daemon.discoverCatalog(t.Context())
	var row map[string]any
	testkit.Eventually(t, "the ask in the starter's waiting list", 10*time.Second, func() bool {
		f.vps.daemon.discoverCatalog(t.Context())
		row = waitingAsk(t, f.starter, askID)
		return row != nil
	})
	if row["sessionId"] != f.created {
		t.Fatalf("waiting row %v, want the worker by its host-qualified id", row)
	}
	parserAskShown(t, "the waiting row", row)
	if row := waitingAsk(t, f.bystander, askID); row != nil {
		t.Fatalf("a session the ask is not routed to lists it: %v", row)
	}
	answer := map[string]any{"requestId": askID, "sessionId": printed, "answers": map[string]any{"Hand-written or generated?": "Hand-written"}}
	if out, refused := callMCP(t, f.starter, "agenthail_answer", answer); refused {
		t.Fatalf("answer by the notice's name: %v", out)
	}
	if row := waitingAsk(t, f.starter, askID); row != nil {
		t.Fatalf("the answered ask is still listed: %v", row)
	}
}

func (f starterAcrossHosts) workerAsk(t *testing.T) string {
	t.Helper()
	out, refused := callMCP(t, f.worker, "agenthail_ask", parserAsk)
	if refused {
		t.Fatalf("ask: %v", out)
	}
	return out["askId"].(string)
}

func TestATurnStopNoticeToALeadOnAPairedHostNamesTheWorkerSoItResolvesThere(t *testing.T) {
	l := leadOnTheOtherHost(t)
	if err := l.laptop.reg.SetAlias("builder", l.worker); err != nil {
		t.Fatal(err)
	}
	if err := l.vps.reg.SetAlias("builder", "vps-b"); err != nil {
		t.Fatal(err)
	}
	worker, err := l.laptop.reg.Session(l.worker)
	if err != nil {
		t.Fatal(err)
	}
	worker.TurnStop = &surface.TurnStop{TurnID: "t1", At: time.Now(), Reason: "the provider failed"}
	l.laptop.daemon.noticeTurnStop(*worker)
	var named string
	testkit.Eventually(t, "the lead told the worker stopped", 10*time.Second, func() bool {
		for _, got := range l.vps.fake.delivered() {
			if match := noticeLast.FindStringSubmatch(got); match != nil && strings.Contains(got, "stopped") {
				named = match[1]
				return true
			}
		}
		return false
	})
	lead := mustMCP(t, l.vps, "vps-a")
	if out, refused := callMCP(t, lead, "agenthail_read", map[string]any{"sessionId": named}); refused || out["sessionId"] != l.workerQ {
		t.Fatalf("the lead reading %s from the notice: refused=%v out=%v, want the worker on the paired host, not its own @builder", named, refused, out)
	}
}
