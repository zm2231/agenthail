package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var updateActionParity = flag.Bool("update-action-parity", false, "rewrite testdata/action_parity.json from the current handlers")

type parityCase struct {
	name   string
	action string
	body   string
	setup  func(t *testing.T, d *Daemon, r *registry.Registry)
}

func withCodexLaunched(t *testing.T, d *Daemon, _ *registry.Registry) {
	t.Helper()
	d.launchCodex = func(context.Context, string) ([]byte, error) { return []byte("Codex is running\n"), nil }
}

func withCodexLaunchFailing(t *testing.T, d *Daemon, _ *registry.Registry) {
	t.Helper()
	d.launchCodex = func(context.Context, string) ([]byte, error) {
		return []byte("Codex is not installed\n"), errors.New("exit status 1")
	}
}

func withChannel(t *testing.T, _ *Daemon, r *registry.Registry) {
	t.Helper()
	if _, err := r.CreateChannel("team"); err != nil {
		t.Fatal(err)
	}
}

func withRelay(t *testing.T, _ *Daemon, r *registry.Registry) {
	t.Helper()
	if _, err := r.AddRoute("from", "to", ".*"); err != nil {
		t.Fatal(err)
	}
}

func withQueued(t *testing.T, _ *Daemon, r *registry.Registry) {
	t.Helper()
	if _, err := r.QueueMessageWithKey("to", "waiting", "parity"); err != nil {
		t.Fatal(err)
	}
}

func parityCases() []parityCase {
	session := func(name, action, extra string) parityCase {
		body := `{"action":"` + action + `","sessionId":"to"` + extra + `}`
		return parityCase{name: name, action: action, body: body}
	}
	cases := []parityCase{
		{name: "invalid json", body: `{"action":`},
		{name: "unknown action", action: "", body: `{"action":"nope","sessionId":"to"}`},
		{name: "no session", action: "", body: `{"action":"send","message":"hello"}`},
		{name: "unknown session", action: "", body: `{"action":"send","sessionId":"ghost","message":"hello"}`},
		{name: "attachments outside send", action: "", body: `{"action":"steer","sessionId":"to","message":"x","attachments":["a"]}`},
		session("send", "send", `,"message":"hello"`),
		session("send empty", "send", ``),
		session("send as person", "send", `,"message":"hello","author":"person","personName":"Ada"`),
		session("send from session", "send", `,"message":"hello","sourceSessionId":"from"`),
		session("send unknown source", "send", `,"message":"hello","sourceSessionId":"ghost"`),
		session("steer", "steer", `,"message":"nudge"`),
		session("interrupt", "interrupt", ``),
		session("compact", "compact", ``),
		session("goal-set", "goal-set", `,"message":"ship it"`),
		session("goal-edit", "goal-edit", `,"message":"ship it"`),
		session("goal-clear", "goal-clear", ``),
		session("goal-pause", "goal-pause", ``),
		session("goal-resume", "goal-resume", ``),
		session("goal-budget", "goal-budget", `,"message":"100"`),
		session("goal-budget invalid", "goal-budget", `,"message":"-1"`),
		session("model", "model", `,"model":"gpt"`),
		session("model empty", "model", ``),
		session("permission-mode", "permission-mode", `,"permissionMode":"plan"`),
		session("respond", "respond", `,"requestId":"r1","decision":{"behavior":"allow"}`),
		session("context", "context", ``),
		session("usage", "usage", ``),
		session("status", "status", ``),
		session("effort", "effort", `,"message":"high"`),
		session("rename", "rename", `,"message":"new name"`),
		session("alias", "alias", `,"alias":"@renamed"`),
		session("alias invalid", "alias", `,"alias":"has space"`),
		session("limit-resume", "limit-resume", ``),
		session("limit-resume-cancel", "limit-resume-cancel", ``),
		session("limit-move", "limit-move", `,"configRoot":"work"`),
		session("session-focus", "session-focus", ``),
		session("session-archive", "session-archive", ``),
		session("session-unarchive", "session-unarchive", ``),
		session("session-adopt", "session-adopt", ``),
		session("session-fork", "session-fork", `,"alias":"forked"`),
		session("native-queue", "native-queue", `,"nativeQueue":{"action":"list"}`),
		session("session-lifecycle-stop", "session-lifecycle-stop", ``),
		session("session-lifecycle-status", "session-lifecycle-status", ``),
		session("session-lifecycle-logs", "session-lifecycle-logs", ``),
		{name: "session-create", action: "session-create", body: `{"action":"session-create","surface":"codex","message":"go","approvalPolicy":"sometimes"}`},
		{name: "session-create unknown creator", action: "session-create", body: `{"action":"session-create","surface":"codex","message":"go","sourceSessionId":"ghost"}`},
		{name: "notion-create", action: "notion-create", body: `{"action":"notion-create","message":"go","alias":"bad name"}`},
		{name: "queue-retry", action: "queue-retry", body: `{"action":"queue-retry","queueId":1}`, setup: withQueued},
		{name: "queue-retry missing", action: "queue-retry", body: `{"action":"queue-retry","queueId":999}`},
		{name: "queue-cancel", action: "queue-cancel", body: `{"action":"queue-cancel","queueId":1}`, setup: withQueued},
		{name: "delivery-dismiss", action: "delivery-dismiss", body: `{"action":"delivery-dismiss","deliveryId":999}`},
		{name: "channel-create", action: "channel-create", body: `{"action":"channel-create","channel":"team"}`},
		{name: "channel-create taken", action: "channel-create", body: `{"action":"channel-create","channel":"team"}`, setup: withChannel},
		{name: "channel-delete", action: "channel-delete", body: `{"action":"channel-delete","channel":"team"}`, setup: withChannel},
		{name: "channel-rename", action: "channel-rename", body: `{"action":"channel-rename","channel":"team","newChannel":"crew"}`, setup: withChannel},
		{name: "channel-add", action: "channel-add", body: `{"action":"channel-add","channel":"team","targetId":"to"}`, setup: withChannel},
		{name: "channel-remove", action: "channel-remove", body: `{"action":"channel-remove","channel":"team","targetId":"to"}`, setup: withChannel},
		{name: "channel-send", action: "channel-send", body: `{"action":"channel-send","channel":"team","message":"standup","sourceSessionId":"from"}`, setup: withChannel},
		{name: "relay-add", action: "relay-add", body: `{"action":"relay-add","fromId":"from","toId":"to","pattern":".*"}`},
		{name: "relay-remove", action: "relay-remove", body: `{"action":"relay-remove","relayId":1}`, setup: withRelay},
		{name: "runtime-ensure", action: "runtime-ensure", body: `{"action":"runtime-ensure"}`},
		{name: "codex-launch", action: "codex-launch", body: `{"action":"codex-launch"}`, setup: withCodexLaunched},
		{name: "codex-launch failing", action: "codex-launch", body: `{"action":"codex-launch"}`, setup: withCodexLaunchFailing},
		{name: "project-add", action: "project-add", body: `{"action":"project-add","path":"relative/path"}`},
		{name: "project-remove", action: "project-remove", body: `{"action":"project-remove","projectId":"missing"}`},
		{name: "project-rename", action: "project-rename", body: `{"action":"project-rename","projectId":"missing","title":"New"}`},
		{name: "project-archive", action: "project-archive", body: `{"action":"project-archive","projectId":"missing"}`},
		{name: "project-unarchive", action: "project-unarchive", body: `{"action":"project-unarchive","projectId":"missing"}`},
		{name: "coordinator-new", action: "coordinator-new", body: `{"action":"coordinator-new","path":"relative/path","surface":"claude"}`},
		{name: "coordinator-set", action: "coordinator-set", body: `{"action":"coordinator-set","sessionId":"to","path":"relative/path"}`},
		{name: "coordinator-clear", action: "coordinator-clear", body: `{"action":"coordinator-clear","path":"relative/path"}`},
		{name: "coordinator-send", action: "coordinator-send", body: `{"action":"coordinator-send","name":"nobody","message":"hi"}`},
	}
	return cases
}

func parityDaemon(t *testing.T, setup func(*testing.T, *Daemon, *registry.Registry)) *Daemon {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, r, fake, _, _ := daemonFixture(t)
	fake.caps = surface.Capabilities{Send: true, Steer: true, Interrupt: true, Compact: true, Goal: true, Model: true, PermissionMode: true, Commands: true, Attach: true}
	d.notifyDesktop = func(string, string, string, string, string, []surface.RequestAction) error { return nil }
	if setup != nil {
		setup(t, d, r)
	}
	return d
}

type parityReply struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType"`
	Body        any    `json:"body"`
}

var parityVolatile = []struct {
	pattern *regexp.Regexp
	value   string
}{
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?( [+-]\d{4} UTC)?`), "<time>"},
	{regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`), "<uuid>"},
}

func normalizeParity(t *testing.T, d *Daemon, raw []byte) any {
	t.Helper()
	text := string(raw)
	if cwd, err := os.Getwd(); err == nil {
		text = strings.ReplaceAll(text, cwd, "<cwd>")
	}
	if host := d.hostIdentity().Name; host != "" {
		text = strings.ReplaceAll(text, host, "<host>")
	}
	for _, dir := range []string{os.TempDir(), filepath.Clean(os.TempDir()), "/private" + filepath.Clean(os.TempDir())} {
		text = strings.ReplaceAll(text, dir, "<tmp>")
	}
	text = regexp.MustCompile(`<tmp>/?[^"\s]*`).ReplaceAllString(text, "<tmp>")
	for _, volatile := range parityVolatile {
		text = volatile.pattern.ReplaceAllString(text, volatile.value)
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return text
	}
	return value
}

func replyOverHTTP(t *testing.T, d *Daemon, body string) actionReply {
	t.Helper()
	recorder := httptest.NewRecorder()
	d.actionsHTTPHandler(actionFormatAPIv1)(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/actions", strings.NewReader(body)))
	return actionReply{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.Bytes()}
}

func TestEveryActionRepliesTheSameThroughEveryFrontEnd(t *testing.T) {
	golden := filepath.Join("testdata", "action_parity.json")
	recorded := map[string]parityReply{}
	if !*updateActionParity {
		raw, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("read %s (record it with -update-action-parity): %v", golden, err)
		}
		if err := json.Unmarshal(raw, &recorded); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]parityReply{}
	for _, c := range parityCases() {
		t.Run(c.name, func(t *testing.T) {
			viaHTTP, viaService := parityDaemon(t, c.setup), parityDaemon(t, c.setup)
			overHTTP := replyOverHTTP(t, viaHTTP, c.body)
			direct := viaService.performAction(context.Background(), principal{ID: "parity", Kind: principalPerson}, actionFormatAPIv1, []byte(c.body), "")
			httpReply := parityReply{Status: overHTTP.Status, ContentType: overHTTP.Header.Get("Content-Type"), Body: normalizeParity(t, viaHTTP, overHTTP.Body)}
			directReply := parityReply{Status: direct.Status, ContentType: direct.Header.Get("Content-Type"), Body: normalizeParity(t, viaService, direct.Body)}
			httpJSON, _ := json.Marshal(httpReply)
			directJSON, _ := json.Marshal(directReply)
			if !bytes.Equal(httpJSON, directJSON) {
				t.Fatalf("HTTP replied %s, the service replied %s", httpJSON, directJSON)
			}
			got[c.name] = httpReply
			if *updateActionParity {
				return
			}
			want, ok := recorded[c.name]
			if !ok {
				t.Fatalf("no recorded reply for %q; record it with -update-action-parity", c.name)
			}
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(httpJSON, wantJSON) {
				t.Fatalf("reply changed:\n got %s\nwant %s", httpJSON, wantJSON)
			}
		})
	}
	if *updateActionParity {
		raw, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheParityTableCoversEveryDispatchedAction(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range parityCases() {
		covered[c.action] = true
	}
	var missing []string
	for _, name := range dispatchedActionNames(t) {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("actions with no parity case: %s", strings.Join(missing, ", "))
	}
}
