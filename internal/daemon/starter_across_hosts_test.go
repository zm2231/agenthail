package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

const gadgetProject = "repo:github.com/example/gadget"

type starterAcrossHosts struct {
	laptop, vps        *federationNode
	vpsRecord          registry.PairedHost
	laptopRecord       registry.PairedHost
	starter, bystander *mcp.ClientSession
	worker             *mcp.ClientSession
	created            string
}

// startAcrossHosts pairs a laptop and a vps both ways. On the laptop,
// laptop-a and laptop-b share the widget project; laptop-a starts @fold on
// the vps in the gadget project, which has no coordinator, and laptop-b
// starts nothing. created is the id the create answered with.
func startAcrossHosts(t *testing.T) starterAcrossHosts {
	t.Helper()
	laptop, vps, vpsRecord, laptopRecord := pairedBothWays(t)
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	moveSession(t, laptop, "laptop-a", widget, time.Now())
	moveSession(t, laptop, "laptop-b", widget, time.Now())
	laptop.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the laptop's sessions mirrored on the vps", 10*time.Second, func() bool {
		return len(sessionsOf(vps.snapshot(t), laptopRecord.ID)) == 2
	})
	f := starterAcrossHosts{laptop: laptop, vps: vps, vpsRecord: vpsRecord, laptopRecord: laptopRecord}
	f.starter, f.bystander = mustMCP(t, laptop, "laptop-a"), mustMCP(t, laptop, "laptop-b")
	out, refused := callMCP(t, f.starter, "agenthail_create", map[string]any{"surface": "codex", "host": "vps", "cwd": widgetRepo(t, "https://github.com/example/gadget.git"), "message": "build the parser", "alias": "fold", "clientRequestId": "start-fold"})
	if refused {
		t.Fatalf("start a worker on the vps: %v", out)
	}
	session, _ := out["session"].(map[string]any)
	f.created, _ = session["id"].(string)
	if f.created != hosts.Qualify(vpsRecord.ID, "started") {
		t.Fatalf("create answered %v, want the worker's host-qualified id", out)
	}
	vps.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the worker mirrored on the laptop with its project and starter", 10*time.Second, func() bool {
		for _, row := range sessionsOf(laptop.snapshot(t), vpsRecord.ID) {
			if row.ID == f.created {
				return row.StartedBy == "laptop-a" && row.Project != nil && row.Project.ID == gadgetProject
			}
		}
		return false
	})
	f.worker = mustMCP(t, vps, "started")
	return f
}

func refusedAs(t *testing.T, what string, out map[string]any, refused bool, codes ...string) {
	t.Helper()
	if !refused || !slices.Contains(codes, out["code"].(string)) {
		t.Fatalf("%s: refused=%v out=%v, want a refusal %v", what, refused, out, codes)
	}
}

func TestAWorkerStartedOnAPairedHostRecordsItsStarterThere(t *testing.T) {
	f := startAcrossHosts(t)
	origin, found, err := f.vps.reg.SessionOrigin("started")
	if err != nil || !found || origin.Via != registry.OriginAgent || f.vps.daemon.hostQualified(origin.CreatorID) != hosts.Qualify(f.laptopRecord.ID, "laptop-a") {
		t.Fatalf("the vps records origin=%+v found=%v err=%v, want laptop-a as the starter", origin, found, err)
	}
}

func TestAStarterReadsItsWorkerOnAPairedHostByEveryNameTheCreateGave(t *testing.T) {
	f := startAcrossHosts(t)
	for _, ref := range []string{f.created, "vps::@fold", "@fold", "started"} {
		out, refused := callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": ref})
		if refused || out["sessionId"] != f.created {
			t.Fatalf("the starter reading %s: refused=%v out=%v", ref, refused, out)
		}
		out, refused = callMCP(t, f.bystander, "agenthail_read", map[string]any{"sessionId": ref})
		refusedAs(t, "a session that did not start it reading "+ref, out, refused, "out_of_scope")
	}
	listed := func(session *mcp.ClientSession, filter string) bool {
		out, refused := callMCP(t, session, "agenthail_sessions", map[string]any{"filter": filter})
		if refused {
			t.Fatalf("sessions: %v", out)
		}
		rows, _ := out["sessions"].([]any)
		return slices.ContainsFunc(rows, func(row any) bool { return row.(map[string]any)["id"] == f.created })
	}
	if !listed(f.starter, "mine") || !listed(f.starter, "all-visible") || listed(f.starter, "project") {
		t.Fatal("the starter's list: want its worker under mine and all-visible, not under its project")
	}
	if listed(f.bystander, "all-visible") {
		t.Fatal("a session that did not start the worker lists it")
	}
	out, refused := callMCP(t, f.starter, "agenthail_session_settings", map[string]any{"target": f.created})
	if refused || out["sessionId"] != f.created {
		t.Fatalf("the starter reading its worker's settings: refused=%v out=%v", refused, out)
	}
}

func TestAWorkersAskReachesItsStarterOnAPairedHostWhichAnswersIt(t *testing.T) {
	f := startAcrossHosts(t)
	asked, refused := callMCP(t, f.worker, "agenthail_ask", map[string]any{"title": "Which parser", "questions": []any{map[string]any{"question": "Hand-written or generated?", "options": []any{map[string]any{"label": "Hand-written"}, map[string]any{"label": "Generated"}}}}})
	if refused {
		t.Fatalf("ask: %v", asked)
	}
	testkit.Eventually(t, "the ask told to the starter on the laptop", 15*time.Second, func() bool {
		return slices.ContainsFunc(f.laptop.fake.delivered(), func(got string) bool { return strings.Contains(got, "asks: Which parser") })
	})
	requestID, _ := asked["askId"].(string)
	if out, refused := callMCP(t, f.starter, "agenthail_requests", map[string]any{"sessionId": f.created}); refused || out["sessionId"] != f.created {
		t.Fatalf("the starter listing its worker's requests: refused=%v out=%v", refused, out)
	}
	out, refused := callMCP(t, f.bystander, "agenthail_requests", map[string]any{"sessionId": f.created})
	refusedAs(t, "a session that did not start it listing its requests", out, refused, "out_of_scope")
	answer := map[string]any{"requestId": requestID, "sessionId": f.created, "answers": map[string]any{"Hand-written or generated?": "Hand-written"}}
	out, refused = callMCP(t, f.bystander, "agenthail_answer", answer)
	refusedAs(t, "a session that did not start it answering", out, refused, "out_of_scope", "forbidden", "refused")
	if out, refused := callMCP(t, f.starter, "agenthail_answer", answer); refused {
		t.Fatalf("the starter answering: %v", out)
	}
	testkit.Eventually(t, "the answer delivered to the worker", 10*time.Second, func() bool {
		return slices.ContainsFunc(f.vps.fake.delivered(), func(got string) bool { return strings.Contains(got, "Hand-written") })
	})
}

func TestAStarterControlsItsWorkerOnAPairedHostInAnotherProject(t *testing.T) {
	f := startAcrossHosts(t)
	for _, tool := range []struct {
		name string
		args map[string]any
	}{
		{"agenthail_steer", map[string]any{"target": f.created, "message": "steer from the starter"}},
		{"agenthail_interrupt", map[string]any{"target": f.created}},
		{"agenthail_send", map[string]any{"target": "vps::@fold", "message": "now from the starter", "mode": "now"}},
	} {
		tool.args["clientRequestId"] = "bystander-" + tool.name
		out, refused := callMCP(t, f.bystander, tool.name, tool.args)
		refusedAs(t, "a session that did not start it: "+tool.name, out, refused, "out_of_scope")
		tool.args["clientRequestId"] = "starter-" + tool.name
		if out, refused := callMCP(t, f.starter, tool.name, tool.args); refused {
			t.Fatalf("the starter: %s: %v", tool.name, out)
		}
	}
	if !f.vpsGot("steer from the starter") || !f.vpsGot("now from the starter") {
		t.Fatalf("delivered on the vps: %q", f.vps.fake.delivered())
	}
}

func TestAStarterArchivesItsWorkerOnAPairedHostInAnotherProject(t *testing.T) {
	f := startAcrossHosts(t)
	moveSession(t, f.vps, "started", f.vps.fake.sessions["started"].Cwd, time.Now().Add(-time.Minute))
	idle := f.vps.fake.sessions["started"]
	idle.Status = surface.StatusIdle
	f.vps.fake.sessions["started"] = idle
	if err := f.vps.reg.RecordObservedSession(idle); err != nil {
		t.Fatal(err)
	}
	f.vps.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the worker idle on the vps", 10*time.Second, func() bool {
		return viewRow(t, f.vps.daemon, "started").Status == surface.StatusIdle
	})
	out, refused := callMCP(t, f.bystander, "agenthail_archive", map[string]any{"target": f.created, "clientRequestId": "bystander-archive"})
	refusedAs(t, "a session that did not start it archiving", out, refused, "out_of_scope")
	if archived, err := f.vps.reg.SessionArchived("started"); err != nil || archived {
		t.Fatalf("archived on the vps after a refusal=%v err=%v", archived, err)
	}
	if out, refused := callMCP(t, f.starter, "agenthail_archive", map[string]any{"target": f.created, "clientRequestId": "starter-archive"}); refused {
		t.Fatalf("the starter archiving: %v", out)
	}
	if archived, err := f.vps.reg.SessionArchived("started"); err != nil || !archived {
		t.Fatalf("archived on the vps=%v err=%v", archived, err)
	}
}

func (f starterAcrossHosts) vpsGot(message string) bool {
	return slices.ContainsFunc(f.vps.fake.delivered(), func(got string) bool { return strings.Contains(got, message) })
}

func (f starterAcrossHosts) laptopGot(message string) bool {
	return slices.ContainsFunc(f.laptop.fake.delivered(), func(got string) bool { return strings.Contains(got, message) })
}

func TestAStarterAndItsWorkerMessageEachOtherAfterTheStarterChangesProject(t *testing.T) {
	f := startAcrossHosts(t)
	moveSession(t, f.laptop, "laptop-a", widgetRepo(t, "https://github.com/example/widget-v2.git"), time.Now())
	f.laptop.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the starter's new project mirrored on the vps", 10*time.Second, func() bool {
		project, _, _ := f.vps.reg.CatalogSessionProject("laptop-a")
		return strings.HasSuffix(hostProjectID(project), "widget-v2")
	})
	if out, refused := callMCP(t, f.starter, "agenthail_send", map[string]any{"target": "vps::@fold", "message": "down to the worker", "clientRequestId": "down"}); refused {
		t.Fatalf("the starter messaging its worker: %v", out)
	}
	if out, refused := callMCP(t, f.worker, "agenthail_send", map[string]any{"target": hosts.Qualify(f.laptopRecord.ID, "laptop-a"), "message": "up to the starter", "clientRequestId": "up"}); refused {
		t.Fatalf("the worker messaging its starter: %v", out)
	}
	if out, refused := callMCP(t, f.starter, "agenthail_interrupt", map[string]any{"target": f.created, "clientRequestId": "interrupt"}); refused {
		t.Fatalf("the starter interrupting its worker: %v", out)
	}
	testkit.Eventually(t, "both messages delivered", 10*time.Second, func() bool {
		return f.vpsGot("down to the worker") && f.laptopGot("up to the starter")
	})
	out, refused := callMCP(t, f.bystander, "agenthail_send", map[string]any{"target": "vps::@fold", "message": "from a bystander", "clientRequestId": "bystander"})
	refusedAs(t, "a session that did not start it messaging", out, refused, "out_of_scope")
	out, refused = callMCP(t, f.worker, "agenthail_send", map[string]any{"target": hosts.Qualify(f.laptopRecord.ID, "laptop-b"), "message": "to a bystander", "clientRequestId": "sideways"})
	refusedAs(t, "the worker messaging a session that did not start it", out, refused, "out_of_scope")
}

func TestTheOwningHostRefusesAForwardedActionFromASessionThatDidNotStartIt(t *testing.T) {
	f := startAcrossHosts(t)
	forward := func(source string) *http.Response {
		t.Helper()
		body := map[string]any{"action": "steer", "sessionId": f.created, "message": "forwarded by " + source, "sourceSessionId": source, "scopeProjectId": widgetProject}
		recorder := f.laptop.request(t, http.MethodPost, "/api/v1/actions", body)
		return recorder.Result()
	}
	refused := forward("laptop-b")
	var refusal struct {
		Error  struct{ Code string } `json:"error"`
		Detail struct{ Rule string } `json:"detail"`
	}
	_ = json.NewDecoder(refused.Body).Decode(&refusal)
	if refused.StatusCode != http.StatusForbidden || refusal.Error.Code != "out_of_scope" || refusal.Detail.Rule != "different_project" {
		t.Fatalf("a forwarded steer from laptop-b: status=%d refusal=%+v", refused.StatusCode, refusal)
	}
	if f.vpsGot("forwarded by laptop-b") {
		t.Fatal("the refused steer reached the worker")
	}
	if admitted := forward("laptop-a"); admitted.StatusCode != http.StatusOK || !f.vpsGot("forwarded by laptop-a") {
		body, _ := io.ReadAll(admitted.Body)
		t.Fatalf("a forwarded steer from the starter: status=%d body=%s", admitted.StatusCode, body)
	}
}

func TestAStarterItsWorkersHostDoesNotMirrorStillReadsAndControlsIt(t *testing.T) {
	local, remote, record := pairedFederation(t)
	moveSession(t, local, "local-b", local.fake.sessions["local-a"].Cwd, time.Now())
	starter, bystander := mustMCP(t, local, "local-a"), mustMCP(t, local, "local-b")
	out, refused := callMCP(t, starter, "agenthail_create", map[string]any{"surface": "codex", "host": "vps", "cwd": widgetRepo(t, "https://github.com/example/gadget.git"), "message": "build the parser", "clientRequestId": "start"})
	if refused {
		t.Fatalf("create: %v", out)
	}
	created := hosts.Qualify(record.ID, "started")
	remote.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the worker mirrored here with its starter", 10*time.Second, func() bool {
		for _, row := range sessionsOf(local.snapshot(t), record.ID) {
			if row.ID == created {
				return row.StartedBy == "local-a"
			}
		}
		return false
	})
	if out, refused := callMCP(t, starter, "agenthail_read", map[string]any{"sessionId": created}); refused {
		t.Fatalf("the starter reading: %v", out)
	}
	if out, refused := callMCP(t, starter, "agenthail_interrupt", map[string]any{"target": created, "clientRequestId": "interrupt"}); refused {
		t.Fatalf("the starter interrupting: %v", out)
	}
	out, refused = callMCP(t, bystander, "agenthail_interrupt", map[string]any{"target": created, "clientRequestId": "bystander"})
	refusedAs(t, "a session that did not start it interrupting", out, refused, "out_of_scope")
}

func mustMCP(t *testing.T, node *federationNode, id string) *mcp.ClientSession {
	t.Helper()
	session, err := node.reg.Session(id)
	if err != nil {
		t.Fatal(err)
	}
	return mcpSession(t, mcpServer(t, node.daemon), issueMCPToken(t, node.daemon, *session))
}

func TestAWorkerStartedFromAnAgentsShellOnAPairedHostRecordsItsStarterThere(t *testing.T) {
	laptop, vps, vpsRecord, laptopRecord := pairedBothWays(t)
	payload, _ := json.Marshal(map[string]any{"action": "session-create", "host": "vps", "surface": "codex", "message": "build the parser", "cwd": t.TempDir(), "sourceSessionId": "laptop-a"})
	answer := askScope(t, laptop.daemon, daemonapi.ScopeRequest{SessionID: "laptop-a", Action: "create", Create: payload}, "127.0.0.1:50000")
	if answer.status >= http.StatusMultipleChoices {
		t.Fatalf("create through the CLI's scope request: status=%d body=%s", answer.status, answer.body)
	}
	origin, found, err := vps.reg.SessionOrigin("started")
	if err != nil || !found || origin.Via != registry.OriginAgent || vps.daemon.hostQualified(origin.CreatorID) != hosts.Qualify(laptopRecord.ID, "laptop-a") {
		t.Fatalf("the vps records origin=%+v found=%v err=%v, want laptop-a as the starter", origin, found, err)
	}
	vps.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the worker mirrored on the laptop with its starter", 10*time.Second, func() bool {
		return slices.ContainsFunc(sessionsOf(laptop.snapshot(t), vpsRecord.ID), func(row dashboardSession) bool {
			return row.ID == hosts.Qualify(vpsRecord.ID, "started") && row.StartedBy == "laptop-a"
		})
	})
}

func TestAStarterPagesItsWorkersHistoryAndBodiesFromThePairedHost(t *testing.T) {
	f := startAcrossHosts(t)
	full := strings.Repeat("é", 15000)
	item, _ := json.Marshal(sessionJournalPayload{ItemID: "long", Kind: "message", Role: "assistant", Body: full[:8000], Truncated: true, BodyRef: "body-long", Version: 1, Op: "upsert", TS: time.Now().Format(time.RFC3339Nano)})
	if _, _, err := f.vps.reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: "started", Kind: "message", ProviderKey: "long", Payload: item, ObservedAt: time.Now(), BodyRef: "body-long", FullBody: []byte(full)}, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	page, refused := callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created})
	items, _ := page["items"].([]any)
	if refused || !slices.ContainsFunc(items, func(item any) bool { return item.(map[string]any)["bodyRef"] == "body-long" }) {
		t.Fatalf("the starter's read of its worker: refused=%v page=%v", refused, page)
	}
	var rebuilt strings.Builder
	for offset := 0; ; {
		chunk, refused := callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created, "bodyRef": "body-long", "offset": offset})
		if refused || chunk["sessionId"] != f.created {
			t.Fatalf("body chunk at %d: %v", offset, chunk)
		}
		rebuilt.WriteString(chunk["text"].(string))
		next, more := chunk["nextOffset"].(float64)
		if !more {
			break
		}
		offset = int(next)
	}
	if rebuilt.String() != full {
		t.Fatalf("rebuilt %d bytes from the paired host, want %d", rebuilt.Len(), len(full))
	}
	out, refused := callMCP(t, f.bystander, "agenthail_read", map[string]any{"sessionId": f.created, "bodyRef": "body-long"})
	refusedAs(t, "a session that did not start it reading a body", out, refused, "out_of_scope")
	out, refused = callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created, "bodyRef": "body-gone"})
	refusedAs(t, "a body the paired host does not hold", out, refused, "not_found")
	out, refused = callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created, "since": 0})
	refusedAs(t, "since on another computer", out, refused, "invalid")
	out, refused = callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created, "before": "0:moved-on"})
	if detail, _ := out["detail"].(map[string]any); !refused || out["code"] != "not_found" || detail["stale"] != true {
		t.Fatalf("a cursor past an item the paired host no longer shows: refused=%v out=%v", refused, out)
	}

	retention := registry.SessionJournalRetention{Count: 2, Bytes: 1024}
	for _, key := range []string{"one", "two", "three"} {
		if _, _, err := f.vps.reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: "started", Kind: "text", ProviderKey: key, Payload: []byte(key)}, retention); err != nil {
			t.Fatal(err)
		}
	}
	window, err := f.vps.reg.SessionJournalAfter("started", 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	gone, refused := callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created, "before": encodeReadCursor(int64(window.EarliestSeq), "")})
	if refused || gone["unavailable"] != "No older history is retained." {
		t.Fatalf("history the paired host no longer retains: refused=%v out=%v", refused, gone)
	}

	f.vps.gate.proxyDown.Store(true)
	f.vps.server.CloseClientConnections()
	out, refused = callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created})
	if !refused || out["code"] != "refused" || out["retryable"] != true {
		t.Fatalf("a read while the paired host does not answer: refused=%v out=%v", refused, out)
	}
}

func TestTheOwningHostRefusesAForgedSenderForAStarterItDoesNotMirror(t *testing.T) {
	local, remote, record := pairedFederation(t)
	moveSession(t, local, "local-b", local.fake.sessions["local-a"].Cwd, time.Now())
	if out, refused := callMCP(t, mustMCP(t, local, "local-a"), "agenthail_create", map[string]any{"surface": "codex", "host": "vps", "cwd": widgetRepo(t, "https://github.com/example/gadget.git"), "message": "build the parser", "clientRequestId": "start"}); refused {
		t.Fatalf("create: %v", out)
	}
	if origin, _, _ := remote.reg.SessionOrigin("started"); !surface.IsUnrepliableSender(origin.CreatorID) {
		t.Fatalf("setup: starter recorded as %q, want a stand-in for a session the vps does not mirror", origin.CreatorID)
	}
	forward := func(source string) *httptest.ResponseRecorder {
		t.Helper()
		return local.request(t, http.MethodPost, "/api/v1/actions", map[string]any{"action": "steer", "sessionId": hosts.Qualify(record.ID, "started"), "message": "forwarded by " + source, "sourceSessionId": source, "scopeProjectId": widgetProject})
	}
	if refused := forward("local-b"); refused.Code != http.StatusForbidden || !strings.Contains(refused.Body.String(), "different_project") {
		t.Fatalf("a forwarded steer from local-b: status=%d body=%s", refused.Code, refused.Body.String())
	}
	if slices.ContainsFunc(remote.fake.delivered(), func(got string) bool { return strings.Contains(got, "forwarded by local-b") }) {
		t.Fatal("the refused steer reached the worker")
	}
	if admitted := forward("local-a"); admitted.Code != http.StatusOK {
		t.Fatalf("a forwarded steer from the starter: status=%d body=%s", admitted.Code, admitted.Body.String())
	}
}

func TestARangedBodyReadThroughAPairedHostIsRefusedOnceTheBodyIsRewritten(t *testing.T) {
	f := startAcrossHosts(t)
	prefix := strings.Repeat("é", 4096)
	write := func(tail string) {
		t.Helper()
		item, _ := json.Marshal(sessionJournalPayload{ItemID: "tool", Kind: "message", Role: "assistant", Body: prefix[:64], Truncated: true, BodyRef: "body-tool", Version: 1, Op: "upsert", TS: time.Now().Format(time.RFC3339Nano)})
		if _, _, err := f.vps.reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: "started", Kind: "message", ProviderKey: "tool", Payload: item, ObservedAt: time.Now(), BodyRef: "body-tool", FullBody: []byte(prefix + tail)}, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	write("first")
	head, refused := callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created, "bodyRef": "body-tool"})
	version, ok := head["version"].(float64)
	if refused || !ok || head["nextOffset"] == nil {
		t.Fatalf("the first chunk from the paired host: refused=%v %v", refused, head)
	}
	write("other")
	out, refused := callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created, "bodyRef": "body-tool", "offset": head["nextOffset"], "version": version})
	refusedAs(t, "the next chunk of a body the paired host rewrote", out, refused, "body_rewritten")
	fresh, refused := callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created, "bodyRef": "body-tool", "offset": head["nextOffset"]})
	if refused || fresh["version"] == version {
		t.Fatalf("a read without a version after the rewrite: refused=%v %v", refused, fresh)
	}

	path := "/api/v1/session-stream-body?id=" + f.created + "&ref=body-tool&start=0&end=16&version=" + strconv.FormatUint(uint64(version), 10)
	if response := f.laptop.request(t, http.MethodGet, path, nil); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"body_rewritten"`) {
		t.Fatalf("the body endpoint routed to the paired host: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestABodyReadFromAPairedHostWithoutBodyVersionsStaysUnpinned(t *testing.T) {
	f := startAcrossHosts(t)
	full := strings.Repeat("é", 6000)
	item, _ := json.Marshal(sessionJournalPayload{ItemID: "tool", Kind: "message", Role: "assistant", Body: full[:64], Truncated: true, BodyRef: "body-old", Version: 1, Op: "upsert", TS: time.Now().Format(time.RFC3339Nano)})
	if _, _, err := f.vps.reg.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: "started", Kind: "message", ProviderKey: "tool", Payload: item, ObservedAt: time.Now(), BodyRef: "body-old", FullBody: []byte(full)}, registry.SessionJournalRetention{Count: 4096, Bytes: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	f.vps.gate.unversionedBodies.Store(true)
	var rebuilt strings.Builder
	for offset := 0; ; {
		chunk, refused := callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created, "bodyRef": "body-old", "offset": offset})
		if _, versioned := chunk["version"]; refused || versioned {
			t.Fatalf("a chunk at %d from a host without body versions: refused=%v %v", offset, refused, chunk)
		}
		rebuilt.WriteString(chunk["text"].(string))
		next, more := chunk["nextOffset"].(float64)
		if !more {
			break
		}
		offset = int(next)
	}
	if rebuilt.String() != full {
		t.Fatalf("rebuilt %d bytes, want %d", rebuilt.Len(), len(full))
	}
	out, refused := callMCP(t, f.starter, "agenthail_read", map[string]any{"sessionId": f.created, "bodyRef": "body-old", "version": 1})
	refusedAs(t, "an expected version a host without body versions cannot check", out, refused, "refused")
}
