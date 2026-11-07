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
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var updateActionParity = flag.Bool("update-action-parity", false, "rewrite testdata/action_parity.json from the current handlers")

type parityCase struct {
	name   string
	action string
	body   string
	setup  func(t *testing.T, d *Daemon, r *registry.Registry)
	caller *principal
	key    string
}

func (c parityCase) principal() principal {
	if c.caller != nil {
		return *c.caller
	}
	return principal{ID: localDashboardPrincipal, Kind: principalPerson}
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
		session("session-pin", "session-pin", ``),
		session("session-unpin", "session-unpin", ``),
		session("session-snooze", "session-snooze", `,"until":"2999-01-01T00:00:00Z"`),
		session("session-snooze finished idle", "session-snooze", `,"wake":"finished"`),
		session("session-snooze empty", "session-snooze", ``),
		session("session-wake", "session-wake", ``),
		session("work-clean", "work-clean", ``),
		session("pr unlink not linked", "pr", `,"op":"unlink","pr":"https://github.com/acme/app/pull/1"`),
		session("pr unknown op", "pr", `,"op":"merge","pr":"1"`),
		session("session-adopt", "session-adopt", ``),
		session("session-fork", "session-fork", `,"alias":"forked"`),
		session("native-queue", "native-queue", `,"nativeQueue":{"queueAction":"list"}`),
		session("session-lifecycle-stop", "session-lifecycle-stop", ``),
		session("session-lifecycle-status", "session-lifecycle-status", ``),
		session("session-lifecycle-logs", "session-lifecycle-logs", ``),
		session("session-lifecycle-resume", "session-lifecycle-resume", ``),
		session("respond stale", "respond", `,"requestId":"gone","decision":{"behavior":"allow"}`),
		session("respond choice", "respond", `,"requestId":"r1","decision":{"choice":"o9"}`),
		session("respond choice stale", "respond", `,"requestId":"gone","decision":{"choice":"o0"}`),
		session("request-withdraw from a current session", "request-withdraw", `,"requestId":"r1"`),
		session("session-fork conflicting points", "session-fork", `,"fork":{"lastTurnId":"a","beforeTurnId":"b"}`),
		session("session-fork unknown creator", "session-fork", `,"sourceSessionId":"ghost"`),
		session("session-fork unknown turn", "session-fork", `,"fork":{"beforeTurnId":"never"}`),
		session("send unreadable attachment", "send", `,"message":"see file","attachments":["missing-upload"]`),
		session("send pinned to a process", "send", `,"message":"hello","pid":4242`),
		session("send busy policy", "send", `,"message":"hello","busyDelivery":"queue"`),
		session("steer as person", "steer", `,"message":"nudge","author":"person","personName":"Ada"`),
		session("steer from session", "steer", `,"message":"nudge","sourceSessionId":"from"`),
		session("steer unknown source", "steer", `,"message":"nudge","sourceSessionId":"ghost"`),
		session("steer empty", "steer", ``),
		{name: "send to a daemon session", action: "send", body: `{"action":"send","sessionId":"to","message":"hello"}`, setup: withDaemonSession},
		{name: "lifecycle stop on a daemon session", action: "session-lifecycle-stop", body: `{"action":"session-lifecycle-stop","sessionId":"to"}`, setup: withDaemonSession},
		{name: "limit-resume stopped", action: "limit-resume", body: `{"action":"limit-resume","sessionId":"to"}`, setup: withLimitStop},
		{name: "limit-resume-cancel stopped", action: "limit-resume-cancel", body: `{"action":"limit-resume-cancel","sessionId":"to"}`, setup: withLimitStop},
		{name: "relay leg from a host", action: "send", body: `{"action":"send","sessionId":"to","message":"relayed","relayHops":1}`, caller: &parityHost, key: "relay-leg"},
		{name: "relay leg from a host naming an unqualified sender", action: "send", body: `{"action":"send","sessionId":"to","message":"relayed","relayHops":1,"sourceSessionId":"from"}`, caller: &parityHost, key: "relay-unqualified"},
		{name: "relay leg from a host without a key", action: "send", body: `{"action":"send","sessionId":"to","message":"relayed","relayHops":1}`, caller: &parityHost},
		{name: "relay leg past the hop limit", action: "send", body: `{"action":"send","sessionId":"to","message":"relayed","relayHops":99}`, caller: &parityHost, key: "relay-far"},
		{name: "relay leg not from a host", action: "send", body: `{"action":"send","sessionId":"to","message":"relayed","relayHops":1}`, key: "relay-person"},
		{name: "model by a paired device", action: "model", body: `{"action":"model","sessionId":"to","model":"gpt"}`, caller: &principal{ID: "phone", Kind: principalDevice, HostName: "phone"}},
		{name: "send with a key", action: "send", body: `{"action":"send","sessionId":"to","message":"hello"}`, key: "send-once"},
		{name: "session-create", action: "session-create", body: `{"action":"session-create","surface":"codex","message":"go","approvalPolicy":"sometimes"}`},
		{name: "session-create unknown creator", action: "session-create", body: `{"action":"session-create","surface":"codex","message":"go","sourceSessionId":"ghost"}`},
		{name: "notion-create", action: "notion-create", body: `{"action":"notion-create","message":"go","alias":"bad name"}`},
		{name: "queue-retry", action: "queue-retry", body: `{"action":"queue-retry","queueId":1}`, setup: withQueued},
		{name: "queue-retry missing", action: "queue-retry", body: `{"action":"queue-retry","queueId":999}`},
		{name: "queue-cancel", action: "queue-cancel", body: `{"action":"queue-cancel","queueId":1}`, setup: withQueued},
		{name: "queue-clear", action: "queue-clear", body: `{"action":"queue-clear","sessionId":"to"}`, setup: withQueued},
		{name: "delivery-dismiss", action: "delivery-dismiss", body: `{"action":"delivery-dismiss","deliveryId":999}`},
		{name: "delivery-delete", action: "delivery-delete", body: `{"action":"delivery-delete","deliveryId":999}`},
		{name: "lead-replacement-ack missing", action: "lead-replacement-ack", body: `{"action":"lead-replacement-ack","replacementId":999}`},
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
	cases = append(append(cases, parityFailureCases()...), parityProjectCases()...)
	return append(cases, parityLaunchCases()...)
}

type paritySurface struct {
	*daemonSurface
	fail map[string]error
}

func (s paritySurface) SessionAction(_ context.Context, session *surface.Session, action string) (map[string]any, error) {
	if err := s.fail["lifecycle"]; err != nil {
		return nil, err
	}
	return map[string]any{"action": action, "sessionId": session.ID}, nil
}

func (s paritySurface) UpdateGoal(context.Context, *surface.Session, surface.GoalUpdate) error {
	return s.fail["goal"]
}

func (s paritySurface) ForkSession(_ context.Context, session *surface.Session, options surface.ForkOptions) (*surface.Session, error) {
	if err := s.fail["fork"]; err != nil {
		return nil, err
	}
	fork := *session
	fork.ID, fork.Name = "forked", "forked from "+session.ID
	return &fork, nil
}

func (s paritySurface) NativeQueue(_ context.Context, session *surface.Session, request surface.NativeQueueRequest) (map[string]any, error) {
	if err := s.fail["native-queue"]; err != nil {
		return nil, err
	}
	return map[string]any{"queueAction": request.Action, "sessionId": session.ID}, nil
}

func (s paritySurface) AdoptSession(_ context.Context, session *surface.Session, _ surface.AdoptRequest) (*surface.AdoptResult, error) {
	if err := s.fail["adopt"]; err != nil {
		return nil, err
	}
	return &surface.AdoptResult{Stopped: "terminal"}, nil
}

func (s paritySurface) Requests(context.Context, *surface.Session) (surface.RequestState, error) {
	if err := s.fail["requests"]; err != nil {
		return surface.RequestState{}, err
	}
	return surface.RequestState{PermissionMode: "default", Requests: []surface.Request{{ID: "r1", Kind: surface.WaitingApproval, Tool: "Bash"}}}, nil
}

func (s paritySurface) Respond(_ context.Context, _ *surface.Session, requestID string, decision surface.Decision) (string, error) {
	if err := s.fail["respond"]; err != nil {
		return "", err
	}
	if requestID != "r1" {
		return "", surface.ErrRequestStale
	}
	return decision.Behavior, nil
}

func (s paritySurface) Control(_ context.Context, session *surface.Session, control surface.ControlRequest) (*surface.ControlResult, error) {
	return &surface.ControlResult{SessionID: session.ID, Action: control.Action, Evidence: surface.EvidenceDelivered, Source: "parity", Output: control.Value}, s.fail["control"]
}

func (s paritySurface) StartSession(ctx context.Context, options surface.SessionStartOptions) (*surface.Session, *surface.SendResult, error) {
	if err := s.fail["start"]; err != nil {
		return nil, nil, err
	}
	return s.daemonSurface.StartSession(ctx, options)
}

func (s paritySurface) List(ctx context.Context) ([]surface.Session, error) {
	if err := s.fail["list"]; err != nil {
		return nil, err
	}
	return s.daemonSurface.List(ctx)
}

func withDaemonSession(t *testing.T, _ *Daemon, r *registry.Registry) {
	t.Helper()
	if err := r.RecordSessionOrigin(registry.SessionOrigin{SessionID: "to", Via: registry.OriginDaemon}); err != nil {
		t.Fatal(err)
	}
}

func withLimitStop(t *testing.T, _ *Daemon, r *registry.Registry) {
	t.Helper()
	session, err := r.Session("to")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RecordLimitStop(*session, "account", surface.LimitStop{TurnID: "refused", RefusedAt: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
}

var parityHost = principal{ID: "paired-host-device", Kind: principalHost, HostID: "hparity", HostName: "parity-host"}

func parityDaemon(t *testing.T, setup func(*testing.T, *Daemon, *registry.Registry)) *Daemon {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, r, fake, _, _ := daemonFixture(t)
	fake.caps = surface.Capabilities{Send: true, Steer: true, Interrupt: true, Compact: true, Goal: true, Model: true, PermissionMode: true, Commands: true, Attach: true}
	d.Surfaces = []surface.Surface{paritySurface{daemonSurface: fake}}
	d.notifyDesktop = func(string, string, string, string, string, []surface.RequestAction) error { return nil }
	if setup != nil {
		setup(t, d, r)
	}
	return d
}

type parityReply struct {
	Status int                 `json:"status"`
	Header map[string][]string `json:"header"`
	Body   string              `json:"body"`
}

var parityFormats = []struct {
	name   string
	format actionFormat
	path   string
}{
	{"api/v1/actions", actionFormatAPIv1, "/api/v1/actions"},
	{"api/action", actionFormatDashboard, "/api/action"},
}

var parityVolatile = []struct {
	pattern *regexp.Regexp
	value   string
}{
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?( [+-]\d{4} UTC)?`), "<time>"},
	{regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}\b`), "<clock>"},
	{regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`), "<uuid>"},
}

var parityTestDir = regexp.MustCompile(`<tmp>/Test[^/"\s]*/\d+`)

func normalizeParity(d *Daemon, raw []byte) string {
	text := string(raw)
	if cwd, err := os.Getwd(); err == nil {
		text = strings.ReplaceAll(text, cwd, "<cwd>")
	}
	if host := d.hostIdentity().Name; host != "" {
		text = strings.ReplaceAll(text, host, "<host>")
	}
	for _, dir := range []string{"/private" + filepath.Clean(os.TempDir()), filepath.Clean(os.TempDir())} {
		text = strings.ReplaceAll(text, dir, "<tmp>")
	}
	text = parityTestDir.ReplaceAllString(text, "<tmp>")
	for _, volatile := range parityVolatile {
		text = volatile.pattern.ReplaceAllString(text, volatile.value)
	}
	return text
}

func parityOf(d *Daemon, reply actionReply) parityReply {
	return parityReply{Status: reply.Status, Header: reply.Header, Body: normalizeParity(d, reply.Body)}
}

func replyOverHTTP(d *Daemon, format actionFormat, path string, c parityCase) actionReply {
	recorder := httptest.NewRecorder()
	p := c.principal()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(c.body))
	request = request.WithContext(p.context(request.Context()))
	if p.Claimed != "" {
		request.Header.Set(ActionActorHeader, p.Claimed)
	}
	if c.key != "" {
		request.Header.Set(actionIdempotencyHeader, c.key)
	}
	d.actionsHTTPHandler(format)(recorder, request)
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
		for _, front := range parityFormats {
			key := front.name + " " + c.name
			t.Run(key, func(t *testing.T) {
				viaHTTP, viaService := parityDaemon(t, c.setup), parityDaemon(t, c.setup)
				httpJSON, _ := json.Marshal(parityOf(viaHTTP, replyOverHTTP(viaHTTP, front.format, front.path, c)))
				directJSON, _ := json.Marshal(parityOf(viaService, viaService.performAction(context.Background(), c.principal(), front.format, []byte(c.body), c.key)))
				if !bytes.Equal(httpJSON, directJSON) {
					t.Fatalf("HTTP replied %s, the service replied %s", httpJSON, directJSON)
				}
				var reply parityReply
				_ = json.Unmarshal(httpJSON, &reply)
				got[key] = reply
				if *updateActionParity {
					return
				}
				want, ok := recorded[key]
				if !ok {
					t.Fatalf("no recorded reply for %q; record it with -update-action-parity", key)
				}
				wantJSON, _ := json.Marshal(want)
				if !bytes.Equal(httpJSON, wantJSON) {
					t.Fatalf("reply changed:\n got %s\nwant %s", httpJSON, wantJSON)
				}
			})
		}
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
	covered, named := map[string]bool{}, map[string]bool{}
	for _, c := range parityCases() {
		if named[c.name] {
			t.Errorf("two parity cases are named %q; the second would overwrite the first's recording", c.name)
		}
		named[c.name] = true
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
