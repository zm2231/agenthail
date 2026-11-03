package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

type replacedLeadRequest struct {
	fixture *claudehosttest.Fixture
	r       *registry.Registry
	d       *Daemon
	handler http.Handler
	phone   string
	id      string
	next    string
	request openRequestView
	spec    registry.CoordinatorSpec
}

// openLeadRequest starts a hosted lead that asks prompt and waits for its request.
func openLeadRequest(t *testing.T, prompt string) *replacedLeadRequest {
	t.Helper()
	f := &replacedLeadRequest{fixture: claudehosttest.New(t, ""), id: uuid.NewString(), next: uuid.NewString()}
	client := f.fixture.Start(t)
	f.fixture.Open(t, client, f.id)
	claude := surfaces.NewClaude(nil, f.fixture.Home)
	session := hostedSession(t, claude, f.id)
	f.r = hostRegistry(t)
	if err := f.r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := f.r.RegisterSession(surface.Session{ID: f.next, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Cwd: session.Cwd}); err != nil {
		t.Fatal(err)
	}
	f.spec = registry.CoordinatorSpec{ProjectID: "project-withdraw", ProjectRoot: session.Cwd, Name: "withdraw-lead", Surface: string(surface.KindClaude), Cwd: session.Cwd}
	if _, err := f.r.SetCoordinator(f.spec, registry.CoordinatorChange{SessionID: f.id}); err != nil {
		t.Fatal(err)
	}
	f.d = newTestDaemon(t, f.r, []surface.Surface{claude})
	f.handler = dashboardRouter(f.d)
	f.phone = deviceToken(t, f.r, "Phone", "read", "control")
	if response := apiCall(t, f.handler, http.MethodPost, "/api/v1/actions", f.phone, map[string]any{"action": "send", "sessionId": f.id, "message": prompt, "author": "person"}); response.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", response.Code, response.Body.String())
	}
	testkit.Eventually(t, "the open request", 15*time.Second, func() bool {
		requests := f.requests(t)
		if len(requests) == 1 {
			f.request = requests[0]
		}
		return len(requests) == 1
	})
	return f
}

func (f *replacedLeadRequest) requests(t *testing.T) []openRequestView {
	t.Helper()
	var state struct {
		Requests []openRequestView `json:"requests"`
	}
	response := apiCall(t, f.handler, http.MethodGet, "/api/v1/requests?sessionId="+f.id, f.phone, nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &state) != nil {
		t.Fatalf("requests status=%d body=%s", response.Code, response.Body.String())
	}
	return state.Requests
}

func (f *replacedLeadRequest) replace(t *testing.T) {
	t.Helper()
	if _, err := f.r.HandOverCoordinator(f.spec, f.id, f.next, registry.CoordinatorHandover{ChangedBy: "daemon", Reason: "gone: conversation no longer exists"}); err != nil {
		t.Fatal(err)
	}
}

func (f *replacedLeadRequest) withdraw(t *testing.T) *httpResponse {
	t.Helper()
	response := apiCall(t, f.handler, http.MethodPost, "/api/v1/actions", f.phone, map[string]any{"action": "request-withdraw", "sessionId": f.id, "requestId": f.request.ID})
	return &httpResponse{code: response.Code, body: response.Body.String()}
}

type httpResponse struct {
	code int
	body string
}

func TestARequestFromTheCurrentLeadCannotBeWithdrawn(t *testing.T) {
	f := openLeadRequest(t, "TOOL")
	if f.request.ReplacedBy != "" {
		t.Fatalf("request=%+v, want no replacement named", f.request)
	}
	if response := f.withdraw(t); response.code != http.StatusConflict || !strings.Contains(response.body, "not_replaced") {
		t.Fatalf("withdraw status=%d body=%s", response.code, response.body)
	}
}

func TestWithdrawingAReplacedLeadsPermissionPromptDeniesItWithANote(t *testing.T) {
	f := openLeadRequest(t, "TOOL")
	f.replace(t)
	if requests := f.requests(t); len(requests) != 1 || requests[0].ReplacedBy != f.next {
		t.Fatalf("requests=%+v, want the request marked as from a lead %s replaced", requests, f.next)
	}
	if response := f.withdraw(t); response.code != http.StatusOK || !strings.Contains(response.body, `"outcome":"denied"`) {
		t.Fatalf("withdraw status=%d body=%s", response.code, response.body)
	}
	testkit.Eventually(t, "the denied turn with its note", 15*time.Second, func() bool {
		turns := f.fixture.Turns(t, f.id)
		return len(turns) == 1 && strings.HasPrefix(turns[0].Reply, "tool:deny:Withdrawn: you are no longer coordinator @withdraw-lead")
	})
}

func TestWithdrawingAReplacedLeadsQuestionDismissesIt(t *testing.T) {
	f := openLeadRequest(t, "QUESTION")
	f.replace(t)
	if response := f.withdraw(t); response.code != http.StatusOK {
		t.Fatalf("withdraw status=%d body=%s", response.code, response.body)
	}
	testkit.Eventually(t, "the dismissed question", 15*time.Second, func() bool {
		turns := f.fixture.Turns(t, f.id)
		return len(turns) == 1 && turns[0].Reply == "question:deny:null"
	})
	if requests := f.requests(t); len(requests) != 0 {
		t.Fatalf("requests after withdraw=%+v", requests)
	}
}

func TestAReplacedLeadsRowSaysWhenItHeldTheName(t *testing.T) {
	f := openLeadRequest(t, "TOOL")
	f.replace(t)
	f.d.discoverCatalog(context.Background())
	view, err := f.d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range view.Sessions {
		switch row.ID {
		case f.id:
			if len(row.ReplacedLead) != 1 || row.ReplacedLead[0].Name != "withdraw-lead" || row.ReplacedLead[0].Until.IsZero() {
				t.Fatalf("replaced lead row=%+v", row.ReplacedLead)
			}
		case f.next:
			if row.ReplacedLead != nil {
				t.Fatalf("current lead row=%+v", row.ReplacedLead)
			}
		}
	}
}

func TestAReplacedLeadsRequestIsShownApartAndDoesNotNeedThePerson(t *testing.T) {
	waiting := &surface.Waiting{ID: "request-1", Kind: surface.WaitingApproval}
	display := &SessionDisplay{Kind: "waiting", Detail: "Answer in Agenthail"}
	lead := dashboardSession{ID: "lead", Display: display, WaitingOn: waiting}
	replaced := dashboardSession{ID: "old", Display: display, WaitingOn: waiting, ReplacedLead: []registry.CoordinatorTenure{{Name: "work-lead"}}}
	if attention := rowAttention(lead, 0, ""); attention == nil || attention.Kind != "request" {
		t.Fatalf("lead attention=%+v", attention)
	}
	replaced.Attention = rowAttention(replaced, 2, "a failed message")
	if replaced.Attention == nil || replaced.Attention.Kind != AttentionReplacedRequest {
		t.Fatalf("replaced lead attention=%+v", replaced.Attention)
	}
	plan := sessionViewPlan{rows: map[string]*dashboardSession{"old": &replaced}}
	if summary := plan.summary([]SessionViewFamily{{RootID: "old", Members: []SessionViewMember{{ID: "old"}}}}); summary.AttentionFamilies != 0 {
		t.Fatalf("summary=%+v, want a replaced lead's request not counted", summary)
	}
}
