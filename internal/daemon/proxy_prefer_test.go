package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/proxyacct/proxyaccttest"
)

const preferFiles = `{"files":[
 {"name":"claude-ops.json","provider":"claude","email":"ops@example.com","status":"active","quota_preference":10},
 {"name":"claude-lead.json","provider":"claude","email":"lead@example.com","status":"active","quota_preference":10},
 {"name":"claude-spare.json","provider":"claude","email":"spare@example.com","status":"active","quota_preference":0},
 {"name":"claude-old.json","provider":"claude","email":"old@example.com","disabled":true,"quota_preference":10},
 {"name":"codex-ops.json","provider":"codex","email":"ops@example.com","status":"active","quota_preference":10}]}`

func newPreferFixture(t *testing.T) (proxyUsageFixture, string) {
	f := newProxyUsageFixture(t, "file")
	f.proxy.Files.Store(preferFiles)
	usage, _ := f.usage(t)
	for _, set := range usage.Sets {
		if set.Provider == "proxy" && set.Proxy != nil {
			return f, set.Proxy.ID
		}
	}
	t.Fatal("no proxy set")
	return f, ""
}

func (f proxyUsageFixture) plan(t *testing.T, proxy, account, set string, now bool) proxyPreferPlan {
	t.Helper()
	var plan proxyPreferPlan
	query := url.Values{"proxy": {proxy}, "account": {account}, "set": {set}}
	if now {
		query.Set("now", "true")
	}
	f.get(t, "/api/v1/proxies/prefer?"+query.Encode(), &plan)
	return plan
}

func (f proxyUsageFixture) prefer(t *testing.T, request proxyPreferRequest) (int, string) {
	t.Helper()
	body, _ := json.Marshal(request)
	post := httptest.NewRequest(http.MethodPost, "/api/v1/proxies/prefer", bytes.NewReader(body))
	post.Header.Set("Authorization", "Bearer secret")
	post.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.daemon.dashboardHandler(&dashboardServer{token: "secret"}).ServeHTTP(response, post)
	return response.Code, response.Body.String()
}

func TestTheToggleChangesOnlyItsOwnAccount(t *testing.T) {
	f, proxy := newPreferFixture(t)
	plan := f.plan(t, proxy, "claude-spare.json", preferOn, false)
	if len(plan.Changes) != 1 || plan.Changes[0] != (proxyPreferChange{ID: "claude-spare.json", Label: "spare@example.com", Provider: "claude", Before: 0, After: 10}) || len(plan.Release) != 0 || plan.Confirm != "Prefer spare@example.com" {
		t.Fatalf("plan=%+v", plan)
	}
	if len(plan.Hosts) != 1 || !slices.Contains(plan.Hosts[0].Roots, f.team) {
		t.Fatalf("the plan names the roots through the proxy: %+v", plan.Hosts)
	}
	code, body := f.prefer(t, proxyPreferRequest{Proxy: proxy, Account: "claude-spare.json", Set: preferOn, PlanID: plan.PlanID})
	if code != http.StatusOK || !strings.Contains(body, "spare@example.com set to 10") {
		t.Fatalf("%d %s", code, body)
	}
	if writes := f.proxy.Writes(); len(writes) != 1 || writes[0] != `fields {"name":"claude-spare.json","quota_preference":10}` {
		t.Fatalf("one write, to that account only: %q", writes)
	}
	usage, _ := f.usage(t)
	for _, set := range usage.Sets {
		for _, account := range set.Accounts {
			if account.ID == "claude-spare.json" && (account.Preference != 10 || account.SetOn != f.daemon.localHost().Name || account.SetAt.IsZero()) {
				t.Fatalf("the cache is refreshed and the change is credited: %+v", account)
			}
			if account.ID == "claude-ops.json" && account.SetOn != "" {
				t.Fatalf("an account Agenthail did not change is not credited: %+v", account)
			}
		}
	}
}

func TestPreferOnlyNamesEveryAccountItTurnsOffAndWritesTheChosenFirst(t *testing.T) {
	f, proxy := newPreferFixture(t)
	plan := f.plan(t, proxy, "claude-spare.json", preferOnly, true)
	ids := []string{}
	for _, change := range plan.Changes {
		ids = append(ids, change.ID)
	}
	if !slices.Equal(ids, []string{"claude-spare.json", "claude-lead.json", "claude-ops.json"}) {
		t.Fatalf("the disabled and Codex accounts are not touched: %+v", plan.Changes)
	}
	if len(plan.Release) != 2 || plan.Confirm != "Prefer only spare@example.com (turns off lead@example.com, ops@example.com)" || len(plan.Warnings) == 0 {
		t.Fatalf("plan=%+v", plan)
	}
	code, body := f.prefer(t, proxyPreferRequest{Proxy: proxy, Account: "claude-spare.json", Set: preferOnly, Now: true, PlanID: plan.PlanID})
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	want := []string{
		`fields {"name":"claude-spare.json","quota_preference":10}`,
		`fields {"name":"claude-lead.json","quota_preference":0}`,
		`fields {"name":"claude-ops.json","quota_preference":0}`,
		`release {"name":"claude-lead.json"}`,
		`release {"name":"claude-ops.json"}`,
	}
	if writes := f.proxy.Writes(); !slices.Equal(writes, want) {
		t.Fatalf("writes=%q", writes)
	}
}

func TestAPlanThatWentStaleWritesNothing(t *testing.T) {
	f, proxy := newPreferFixture(t)
	plan := f.plan(t, proxy, "claude-spare.json", preferOn, false)
	f.proxy.Files.Store(strings.Replace(preferFiles, `"lead@example.com","status":"active","quota_preference":10`, `"lead@example.com","status":"active","quota_preference":0`, 1))
	code, body := f.prefer(t, proxyPreferRequest{Proxy: proxy, Account: "claude-spare.json", Set: preferOn, PlanID: plan.PlanID})
	if code != http.StatusConflict || !strings.Contains(body, "plan_stale") || len(f.proxy.Writes()) != 0 {
		t.Fatalf("%d %s writes=%q", code, body, f.proxy.Writes())
	}
}

func TestAFailedStepStopsAndSaysWhatChanged(t *testing.T) {
	f, proxy := newPreferFixture(t)
	f.proxy.FailWrite.Store(`claude-lead.json 500 {"error":"failed to persist auth file"}`)
	plan := f.plan(t, proxy, "claude-spare.json", preferOnly, false)
	code, body := f.prefer(t, proxyPreferRequest{Proxy: proxy, Account: "claude-spare.json", Set: preferOnly, PlanID: plan.PlanID})
	if code != http.StatusBadGateway || !strings.Contains(body, "spare@example.com set to 10; lead@example.com not changed: ") || !strings.Contains(body, "failed to persist auth file") || !strings.Contains(body, "ops@example.com not changed. Run it again to finish.") {
		t.Fatalf("%d %s", code, body)
	}
	if writes := f.proxy.Writes(); len(writes) != 2 {
		t.Fatalf("the sequence stops at the failure and nothing is rolled back: %q", writes)
	}
}

func TestAReadOnlyKeyIsNamedAndNeverPrinted(t *testing.T) {
	f, proxy := newPreferFixture(t)
	f.proxy.FailWrite.Store(`claude-spare.json 403 {"error":"read-only management key"}`)
	plan := f.plan(t, proxy, "claude-spare.json", preferOn, false)
	code, body := f.prefer(t, proxyPreferRequest{Proxy: proxy, Account: "claude-spare.json", Set: preferOn, PlanID: plan.PlanID})
	if code != http.StatusBadGateway || !strings.Contains(body, "can read the shared proxy but not change it") || strings.Contains(body, proxyaccttest.Key) {
		t.Fatalf("%d %s", code, body)
	}
	usage, _ := f.usage(t)
	for _, set := range usage.Sets {
		if set.Provider == "proxy" && set.ProxyProblem != "" {
			t.Fatalf("a read-only refusal does not stop reads: %+v", set)
		}
	}
}

func TestAPreviewNeedsTheSettingsScopeAndAWriteNeedsAPlan(t *testing.T) {
	f, proxy := newPreferFixture(t)
	code, body := f.prefer(t, proxyPreferRequest{Proxy: proxy, Account: "claude-spare.json", Set: preferOn})
	if code != http.StatusBadRequest || !strings.Contains(body, "plan_required") {
		t.Fatalf("%d %s", code, body)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/proxies/prefer?forwarded=1&proxy="+url.QueryEscape(proxy), nil)
	response := httptest.NewRecorder()
	f.daemon.dashboardHandler(&dashboardServer{token: "secret"}).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("a forwarded request needs a host token: %d", response.Code)
	}
	if code, body := f.prefer(t, proxyPreferRequest{Proxy: "http://elsewhere:1", Account: "x", Set: preferOn, PlanID: "x"}); code != http.StatusNotFound || !strings.Contains(body, "no_proxy") {
		t.Fatalf("%d %s", code, body)
	}
	if len(f.proxy.Writes()) != 0 {
		t.Fatal(f.proxy.Writes())
	}
}

func TestPreferredMarksTheTopOfEachProvider(t *testing.T) {
	f, _ := newPreferFixture(t)
	usage, _ := f.usage(t)
	marked := map[string]bool{}
	for _, set := range usage.Sets {
		for _, account := range set.Accounts {
			marked[account.ID] = account.Preferred
		}
	}
	if !marked["claude-ops.json"] || !marked["claude-lead.json"] || marked["claude-spare.json"] || marked["claude-old.json"] || marked["codex-ops.json"] {
		t.Fatalf("two preferred Claude accounts are both marked; a lone Codex account is not: %v", marked)
	}
}
