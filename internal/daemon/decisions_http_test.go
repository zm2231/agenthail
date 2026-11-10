package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func readDecisions(t *testing.T, d *Daemon, etag string) (decisionsPage, *httptest.ResponseRecorder) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/decisions", nil)
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response := httptest.NewRecorder()
	d.decisionsHandler(response, request)
	var page decisionsPage
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
	}
	return page, response
}

func (f *mcpAnswerFixture) waitOnPrompt(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		waiting := &surface.Waiting{Kind: surface.WaitingApproval, ID: id + "-prompt", Answerable: true, Since: time.Now()}
		session := f.surface.sessions[id]
		session.Status, session.WaitingOn = surface.StatusBusy, waiting
		f.surface.sessions[id] = session
		f.surface.observations[id] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t-" + id, WaitingOn: waiting}
	}
	testkit.Eventually(t, "the waits in the session view", 5*time.Second, func() bool {
		f.d.scanAndRelay(t.Context())
		f.d.discoverCatalog(t.Context())
		rows, _, err := f.d.decisionRows(t.Context())
		if err != nil {
			return false
		}
		waiting := map[string]bool{}
		for _, row := range rows {
			waiting[row.ID] = row.WaitingOn != nil
		}
		for _, id := range ids {
			if !waiting[id] {
				return false
			}
		}
		return true
	})
}

func TestAWorkersPromptIsHandledByItsLeadUntilTheLeadIsWaitingOnThePersonToo(t *testing.T) {
	f := newMCPAnswerFixture(t)
	f.waitOnPrompt(t, "worker")
	page, response := readDecisions(t, f.d, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if len(page.Decisions) != 0 || len(page.LeadHandling) != 1 || page.LeadHandling[0].SessionID != "worker" || page.LeadHandling[0].HeldBy != "lead" || page.LeadHandling[0].ProjectID != "p-answer" {
		t.Fatalf("with the lead free: page=%+v", page)
	}
	if _, again := readDecisions(t, f.d, response.Header().Get("ETag")); again.Code != http.StatusNotModified {
		t.Fatalf("an unchanged page answered %d to its own ETag", again.Code)
	}

	f.waitOnPrompt(t, "worker", "lead")
	if _, err := f.r.QueueMessageWithOptions("lead", "the build is ready for you", "worker-report", surface.SendOptions{SourceSessionID: "worker"}); err != nil {
		t.Fatal(err)
	}
	page, _ = readDecisions(t, f.d, "")
	if len(page.LeadHandling) != 0 || len(page.Decisions) != 2 {
		t.Fatalf("with the lead waiting on the person: page=%+v", page)
	}
	lead, worker := page.Decisions[0], page.Decisions[1]
	if lead.SessionID != "lead" || lead.Group != decisionHolding || len(lead.HoldsUp) != 1 || lead.HoldsUp[0] != "worker" {
		t.Fatalf("lead=%+v", lead)
	}
	if !lead.Answerable || len(lead.Requests) != 1 || lead.Requests[0].ID != askIDOfRequest(t, f.r, "lead", "lead-prompt") || len(lead.Requests[0].Actions) == 0 {
		t.Fatalf("the lead's request is not inline with its buttons: %+v", lead.Requests)
	}
	if worker.SessionID != "worker" || worker.Group != decisionWaiting || len(worker.Requests) != 1 || worker.Requests[0].ID != askIDOfRequest(t, f.r, "worker", "worker-prompt") {
		t.Fatalf("worker=%+v", worker)
	}
}

func TestDecisionsForAPairedHostAreAnsweredThroughItAndAnUnknownHostIsRefused(t *testing.T) {
	local, _, record := pairedFederation(t)
	forwarded := local.request(t, http.MethodGet, "/api/v1/decisions?host="+record.ID, nil)
	if forwarded.Code != http.StatusOK || !strings.Contains(forwarded.Body.String(), `"decisions":[]`) {
		t.Fatalf("status=%d body=%s", forwarded.Code, forwarded.Body.String())
	}
	unknown := local.request(t, http.MethodGet, "/api/v1/decisions?host=nowhere", nil)
	if unknown.Code != http.StatusNotFound || !strings.Contains(unknown.Body.String(), "host_not_found") {
		t.Fatalf("an unknown host answered %d: %s", unknown.Code, unknown.Body.String())
	}
}

func TestTheDecisionsEndpointOnlyAnswersGet(t *testing.T) {
	f := newMCPAnswerFixture(t)
	response := httptest.NewRecorder()
	f.d.decisionsHandler(response, httptest.NewRequest(http.MethodPost, "/api/v1/decisions", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", response.Code)
	}
}
