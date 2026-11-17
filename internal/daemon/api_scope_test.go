package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type scopeAnswer struct {
	status int
	reply  ScopeReply
	code   string
	rule   string
	body   []byte
}

func askScope(t *testing.T, d *Daemon, request ScopeRequest, remoteAddr string) scopeAnswer {
	t.Helper()
	body, _ := json.Marshal(request)
	req := httptest.NewRequest(http.MethodPost, ScopePath, bytes.NewReader(body))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	d.apiScopeHandler(rec, req)
	answer := scopeAnswer{status: rec.Code, body: rec.Body.Bytes()}
	if rec.Code < http.StatusMultipleChoices {
		_ = json.Unmarshal(rec.Body.Bytes(), &answer.reply)
		return answer
	}
	var refused struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Detail map[string]any `json:"detail"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &refused)
	answer.code = refused.Error.Code
	answer.rule, _ = refused.Detail["rule"].(string)
	return answer
}

func TestTheScopeEndpointDecidesAsTheSessionsMCPToolsDo(t *testing.T) {
	d, r, fake, worker, lead := daemonFixture(t)
	widget := widgetRepo(t, "https://github.com/example/widget.git")
	gadget := widgetRepo(t, "https://github.com/example/gadget.git")
	outsider := surface.Session{ID: "outsider", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: gadget}
	worker.Cwd, lead.Cwd = widget, widget
	peer := surface.Session{ID: "peer", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", Cwd: widget}
	shelved := peer
	shelved.ID = "shelved"
	for _, session := range []surface.Session{worker, lead, outsider, peer, shelved} {
		session.LastActive = time.Now()
		fake.sessions[session.ID] = session
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
	}
	d.discoverCatalog(context.Background())
	project, err := broadcast.FindProject(r, "widget")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetCoordinator(registry.CoordinatorSpec{ProjectID: project.ID, ProjectRoot: widget, Name: "widget-lead", Surface: string(surface.KindClaude), Cwd: widget}, registry.CoordinatorChange{SessionID: lead.ID, ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := r.ArchiveSession(shelved.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	const local = "127.0.0.1:50000"

	cases := []struct {
		name    string
		request ScopeRequest
		status  int
		code    string
		rule    string
	}{
		{"a worker's queued send to its lead", ScopeRequest{SessionID: worker.ID, Action: "send", Mode: "queue", Target: lead.ID}, http.StatusOK, "", ""},
		{"a worker steering its lead", ScopeRequest{SessionID: worker.ID, Action: "control", Target: lead.ID}, http.StatusForbidden, "out_of_scope", ""},
		{"a worker sending its lead now", ScopeRequest{SessionID: worker.ID, Action: "send", Mode: "now", Target: lead.ID}, http.StatusForbidden, "out_of_scope", ""},
		{"a worker's queued send to a peer in its project", ScopeRequest{SessionID: worker.ID, Action: "send", Mode: "queue", Target: peer.ID}, http.StatusOK, "", ""},
		{"a worker sending a peer now", ScopeRequest{SessionID: worker.ID, Action: "send", Mode: "now", Target: peer.ID}, http.StatusForbidden, "out_of_scope", "not_spawned_or_led"},
		{"a worker steering a peer", ScopeRequest{SessionID: worker.ID, Action: "control", Target: peer.ID}, http.StatusForbidden, "out_of_scope", "not_spawned_or_led"},
		{"a worker's queued send to an archived peer", ScopeRequest{SessionID: worker.ID, Action: "send", Mode: "queue", Target: shelved.ID}, http.StatusForbidden, "out_of_scope", "archived"},
		{"another project's agent queueing to a worker", ScopeRequest{SessionID: outsider.ID, Action: "send", Mode: "queue", Target: worker.ID}, http.StatusForbidden, "out_of_scope", "not_spawned_or_led"},
		{"the lead controlling its worker", ScopeRequest{SessionID: lead.ID, Action: "control", Target: worker.ID}, http.StatusOK, "", ""},
		{"a session acting on itself", ScopeRequest{SessionID: worker.ID, Action: "control", Target: worker.ID}, http.StatusOK, "", ""},
		{"another project's agent controlling a worker", ScopeRequest{SessionID: outsider.ID, Action: "control", Target: worker.ID}, http.StatusForbidden, "out_of_scope", ""},
		{"another project's agent queueing to its coordinator", ScopeRequest{SessionID: outsider.ID, Action: "lead-send", Mode: "queue", Project: "widget"}, http.StatusOK, "", ""},
		{"another project's agent sending its coordinator now", ScopeRequest{SessionID: outsider.ID, Action: "lead-send", Mode: "now", Project: "widget"}, http.StatusForbidden, "out_of_scope", "different_project"},
		{"a member posting to its project", ScopeRequest{SessionID: worker.ID, Action: "channel-send", Channel: "#widget"}, http.StatusOK, "", ""},
		{"another project's agent posting", ScopeRequest{SessionID: outsider.ID, Action: "channel-send", Channel: "#widget"}, http.StatusForbidden, "out_of_scope", "different_project"},
		{"another project's agent posting by this host's qualified name", ScopeRequest{SessionID: outsider.ID, Action: "channel-send", Channel: d.hostIdentity().Name + "::#widget"}, http.StatusForbidden, "out_of_scope", "different_project"},
		{"an agent clearing a queue", ScopeRequest{SessionID: worker.ID, Action: "operator", Payload: scopePayload("action", `{"action":"queue-clear","sessionId":"`+worker.ID+`"}`)}, http.StatusForbidden, "capability_missing", "person_only"},
		{"a worker relaying within its own project", ScopeRequest{SessionID: worker.ID, Action: "operator", Payload: scopePayload("action", `{"action":"relay-add","fromId":"`+worker.ID+`","toId":"`+lead.ID+`"}`)}, http.StatusForbidden, "capability_missing", "person_only"},
		{"the coordinator relaying within its project", ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: scopePayload("action", `{"action":"relay-add","fromId":"`+worker.ID+`","toId":"`+lead.ID+`"}`)}, http.StatusOK, "", ""},
		{"the coordinator relaying from another project", ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: scopePayload("action", `{"action":"relay-add","fromId":"`+outsider.ID+`","toId":"`+worker.ID+`"}`)}, http.StatusForbidden, "capability_missing", "person_only"},
		{"the coordinator relaying an unknown session", ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: scopePayload("action", `{"action":"relay-add","fromId":"ghost","toId":"`+worker.ID+`"}`)}, http.StatusForbidden, "capability_missing", "person_only"},
		{"the coordinator choosing a coordinator", ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: scopePayload("action", `{"action":"coordinator-clear","projectId":"widget"}`)}, http.StatusForbidden, "capability_missing", "person_only"},
		{"the coordinator adding to a channel named for its project", ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: scopePayload("action", `{"action":"channel-add","channel":"#widget","targetId":"`+worker.ID+`"}`)}, http.StatusForbidden, "capability_missing", "person_only"},
		{"the coordinator adding to a channel addressed by its project's id", ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: scopePayload("action", `{"action":"channel-add","channel":"#`+project.ID+`","targetId":"`+worker.ID+`"}`)}, http.StatusForbidden, "capability_missing", "person_only"},
		{"the coordinator renaming its project's channel", ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: scopePayload("action", `{"action":"channel-rename","channel":"#widget","newChannel":"widgets"}`)}, http.StatusForbidden, "capability_missing", "person_only"},
		{"the coordinator renaming another project's channel", ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: scopePayload("action", `{"action":"channel-rename","channel":"#gadget","newChannel":"gadgets"}`)}, http.StatusForbidden, "capability_missing", "person_only"},
		{"an action that cannot be asked for", ScopeRequest{SessionID: lead.ID, Action: "operator", Payload: scopePayload("action", `{"action":"session-archive","sessionId":"`+worker.ID+`"}`)}, http.StatusBadRequest, "invalid", ""},
		{"an operator check without its payload", ScopeRequest{SessionID: lead.ID, Action: "operator"}, http.StatusBadRequest, "invalid", ""},
		{"an unknown session", ScopeRequest{SessionID: "ghost", Action: "control", Target: worker.ID}, http.StatusNotFound, "not_found", ""},
		{"an unknown action", ScopeRequest{SessionID: worker.ID, Action: "fly"}, http.StatusBadRequest, "invalid", ""},
	}
	refusedOutOfScope := map[string]int{}
	for _, c := range cases {
		answer := askScope(t, d, c.request, local)
		if answer.status != c.status || answer.code != c.code || (c.rule != "" && answer.rule != c.rule) {
			t.Errorf("%s: %+v, want %d %s %s", c.name, answer, c.status, c.code, c.rule)
		}
		if c.code == "out_of_scope" {
			refusedOutOfScope[c.request.SessionID+" cli "+c.request.Action]++
		}
	}
	audited := map[string]int{}
	for _, entry := range mcpAudits(t, r) {
		command, rule, _ := strings.Cut(entry.Message, " rule=")
		if entry.Result != "out_of_scope" || !strings.HasPrefix(command, "cli ") || rule == "" || entry.SessionID == "" || entry.Error == "" {
			t.Fatalf("audit %+v, want an out_of_scope refusal naming the command, rule, target and reason", entry)
		}
		audited[entry.SourceSessionID+" "+command]++
	}
	if !reflect.DeepEqual(audited, refusedOutOfScope) {
		t.Fatalf("audited %v, want one entry per scope refusal %v and none for other refusals", audited, refusedOutOfScope)
	}

	for _, payload := range []string{`{"surface":"notion","message":"m"}`, `{"surface":"codex","cwd":"relative","message":"m"}`} {
		if refused := askScope(t, d, ScopeRequest{SessionID: worker.ID, Action: "create", Create: json.RawMessage(payload)}, local); refused.status != http.StatusBadRequest || refused.code != "invalid" {
			t.Fatalf("create %s: %+v", payload, refused)
		}
	}
	if refused := askScope(t, d, ScopeRequest{SessionID: worker.ID, Action: "control", Target: lead.ID}, "192.0.2.1:4000"); refused.status != http.StatusForbidden || refused.code != "remote_refused" {
		t.Fatalf("a request from another machine: %+v", refused)
	}
}

func scopePayload(api, body string) *PersonAction {
	return &PersonAction{API: api, Body: json.RawMessage(body)}
}
