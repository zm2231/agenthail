package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

type lockedLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// retiringFixture is a daemon with hosted Claude sessions, each opened on the
// host of one version, that later versions' updates leave on retiring hosts.
type retiringFixture struct {
	t       *testing.T
	ctx     context.Context
	f       *claudehosttest.Fixture
	claude  *surfaces.Claude
	r       *registry.Registry
	d       *Daemon
	log     *lockedLog
	client  claudehost.Client
	version int
}

func newRetiringFixture(t *testing.T) *retiringFixture {
	t.Helper()
	f := claudehosttest.New(t, "")
	x := &retiringFixture{t: t, ctx: context.Background(), f: f, claude: surfaces.NewClaude(nil, f.Home), r: hostRegistry(t), log: &lockedLog{}}
	x.d = newTestDaemon(t, x.r, []surface.Surface{x.claude})
	x.d.log = log.New(x.log, "", 0)
	x.update()
	return x
}

// update starts the next version's host, which retires the one before.
func (x *retiringFixture) update() {
	x.t.Helper()
	x.version++
	manager := x.f.Manager(x.t, fmt.Sprintf("1.%d.0", x.version))
	client, err := manager.Ensure(x.ctx)
	if err != nil {
		x.t.Fatal(err)
	}
	x.client = client
	x.d.SetClaudeHost(manager)
}

type retiringSession struct {
	session surface.Session
	pid     int
	host    int
}

// open starts a session on the current host whose transcript ends with a
// request of tokens, written with an hour's cache at requestAt.
func (x *retiringFixture) open(tokens int64, requestAt time.Time) retiringSession {
	x.t.Helper()
	hello, err := x.client.Hello(x.ctx)
	if err != nil {
		x.t.Fatal(err)
	}
	id := uuid.NewString()
	opened := x.f.Open(x.t, x.client, id)
	session := hostedSession(x.t, x.claude, id)
	if err := x.r.RegisterSession(session); err != nil {
		x.t.Fatal(err)
	}
	if _, err := x.d.dispatcher().DeliverWithOptions(x.ctx, x.claude, &session, "hello", "", surface.SendOptions{Author: surface.AuthorPerson}); err != nil {
		x.t.Fatal(err)
	}
	testkit.Eventually(x.t, "the turn in the transcript", 15*time.Second, func() bool {
		session = hostedSession(x.t, x.claude, id)
		data, _ := os.ReadFile(session.Transcript)
		return session.Transcript != "" && strings.Contains(string(data), `"type":"assistant"`)
	})
	testkit.Eventually(x.t, "the turn to end", 15*time.Second, func() bool { return hostState(x.t, x.client, id) == "idle" })
	x.request(session, tokens, requestAt)
	session.Status = surface.StatusIdle
	raw, _ := json.Marshal(session)
	if _, _, err := x.r.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: string(raw)}, registry.CatalogEvent{DedupeKey: id + requestAt.String(), Type: "session.upserted", EntityID: id, Payload: json.RawMessage(`{}`)}); err != nil {
		x.t.Fatal(err)
	}
	return retiringSession{session: session, pid: opened.PID, host: hello.PID}
}

func (x *retiringFixture) request(session surface.Session, tokens int64, at time.Time) {
	x.t.Helper()
	line := fmt.Sprintf(`{"type":"assistant","uuid":%q,"timestamp":%q,"sessionId":%q,"message":{"id":"msg-%s","model":"claude-fixture","role":"assistant","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":10,"cache_creation_input_tokens":5000,"cache_read_input_tokens":%d,"output_tokens":5,"cache_creation":{"ephemeral_1h_input_tokens":5000,"ephemeral_5m_input_tokens":0}}}}`+"\n", uuid.NewString(), at.UTC().Format(time.RFC3339Nano), session.ID, uuid.NewString(), tokens-5010)
	transcript, err := os.OpenFile(session.Transcript, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		x.t.Fatal(err)
	}
	defer transcript.Close()
	if _, err := transcript.WriteString(line); err != nil {
		x.t.Fatal(err)
	}
}

// compactDue queues each due idle compaction at now and delivers it.
func (x *retiringFixture) compactDue(now time.Time, read map[string]idleTranscript, sessions ...retiringSession) {
	x.t.Helper()
	x.d.compactIdle(x.ctx, now, read)
	for _, s := range sessions {
		session := s.session
		x.d.drainMessageQueue(x.ctx, x.claude, &session)
	}
}

func (x *retiringFixture) compactTurn(s retiringSession) claudehosttest.Turn {
	x.t.Helper()
	var compact claudehosttest.Turn
	testkit.Eventually(x.t, "the idle compact of "+s.session.ID, 20*time.Second, func() bool {
		turns := x.f.Turns(x.t, s.session.ID)
		i := slices.IndexFunc(turns, func(turn claudehosttest.Turn) bool { return turn.Text == "/compact" })
		if i < 0 {
			return false
		}
		compact = turns[i]
		return true
	})
	return compact
}

func (x *retiringFixture) gone(what string, pids ...int) {
	x.t.Helper()
	testkit.Eventually(x.t, what, 15*time.Second, func() bool {
		for _, pid := range pids {
			if claudehosttest.Alive(pid) {
				return false
			}
		}
		return true
	})
}

func TestAnUpdateKeepsAWarmIdleSessionOnTheRetiringHostUntilItsIdleCompactionThenLetsTheHostLeave(t *testing.T) {
	x := newRetiringFixture(t)
	now := time.Now()
	warm := x.open(250_000, now.Add(-10*time.Minute))
	x.update()
	read := map[string]idleTranscript{}
	x.d.compactIdle(x.ctx, now, read)
	time.Sleep(time.Second)
	if !claudehosttest.Alive(warm.host) || !claudehosttest.Alive(warm.pid) {
		t.Fatal("the retiring host closed a session the daemon keeps for its idle compaction")
	}
	if !strings.Contains(x.log.String(), "keeping ") {
		t.Fatalf("log = %q, want the session kept", x.log.String())
	}

	x.compactDue(now.Add(41*time.Minute), read, warm)
	if compact := x.compactTurn(warm); compact.PID != warm.pid {
		t.Fatalf("the idle compact ran in Claude %d, want the kept Claude %d on the retiring host", compact.PID, warm.pid)
	}
	testkit.Eventually(t, "the compact to end", 15*time.Second, func() bool { return hostState(t, x.client, warm.session.ID) == "idle" })
	x.d.compactIdle(x.ctx, now.Add(42*time.Minute), read)
	x.gone("the retiring host to leave once its session has compacted", warm.host, warm.pid)
	if logged := x.log.String(); !strings.Contains(logged, "it has compacted since its last request") {
		t.Fatalf("log = %q, want the release after the compaction", logged)
	}
}

func TestAnUpdateClosesASmallIdleSessionAtOnce(t *testing.T) {
	x := newRetiringFixture(t)
	now := time.Now()
	small := x.open(50_000, now.Add(-10*time.Minute))
	x.update()
	x.d.compactIdle(x.ctx, now, map[string]idleTranscript{})
	x.gone("the retiring host to close the small session and leave", small.host, small.pid)
	if logged := x.log.String(); !strings.Contains(logged, "under the 200000-token threshold") {
		t.Fatalf("log = %q, want the release and its reason", logged)
	}
}

func TestAPersonsMessageToAKeptSessionRunsOnTheRetiringHostAndTheSessionMovesOnceItsCacheExpires(t *testing.T) {
	x := newRetiringFixture(t)
	now := time.Now()
	kept := x.open(250_000, now.Add(-10*time.Minute))
	x.update()
	read := map[string]idleTranscript{}
	x.d.compactIdle(x.ctx, now, read)

	session := kept.session
	if _, err := x.d.dispatcher().DeliverWithOptions(x.ctx, x.claude, &session, "while kept", "", surface.SendOptions{Author: surface.AuthorPerson}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the person's turn", 15*time.Second, func() bool {
		turns := x.f.Turns(t, kept.session.ID)
		return len(turns) == 2 && turns[1].Reply == "echo:while kept"
	})
	if turn := x.f.Turns(t, kept.session.ID)[1]; turn.PID != kept.pid {
		t.Fatalf("the person's turn ran in Claude %d, want the kept Claude %d", turn.PID, kept.pid)
	}
	held, err := x.client.Open(x.ctx, claudehost.OpenRequest{Resume: kept.session.ID, Cwd: x.f.Cwd})
	if err != nil || held.PID != kept.pid {
		t.Fatalf("open while kept = %+v, %v; want the kept Claude %d", held, err, kept.pid)
	}
	if spawns := x.f.Spawns(t, kept.session.ID); len(spawns) != 1 {
		t.Fatalf("Claude started %d times for the session, want once", len(spawns))
	}
	testkit.Eventually(t, "the turn to end", 15*time.Second, func() bool { return hostState(t, x.client, kept.session.ID) == "idle" })
	x.request(kept.session, 250_000, now)
	x.d.compactIdle(x.ctx, now.Add(30*time.Minute), read)
	time.Sleep(500 * time.Millisecond)
	if !claudehosttest.Alive(kept.pid) {
		t.Fatal("the session left the retiring host inside the cache hour of its newest request")
	}

	x.d.compactIdle(x.ctx, now.Add(61*time.Minute), read)
	x.gone("the retiring host to close the session once its cache expired, and leave", kept.host, kept.pid)
	if logged := x.log.String(); !strings.Contains(logged, "its prompt cache has expired") {
		t.Fatalf("log = %q, want the release once the cache expired", logged)
	}
}

func TestAKeptSessionsCompactReachesItsOwnHostWhenTwoHostsRetire(t *testing.T) {
	x := newRetiringFixture(t)
	now := time.Now()
	first := x.open(250_000, now.Add(-10*time.Minute))
	x.update()
	x.d.compactIdle(x.ctx, now, map[string]idleTranscript{})
	second := x.open(300_000, now.Add(-5*time.Minute))
	x.update()
	read := map[string]idleTranscript{}
	x.d.compactIdle(x.ctx, now, read)
	if retiring := x.client.Retiring(); len(retiring) != 2 {
		t.Fatalf("retiring hosts = %v, want both earlier hosts", retiring)
	}
	if first.host == second.host {
		t.Fatalf("both sessions opened on host %d", first.host)
	}

	x.compactDue(now.Add(41*time.Minute), read, first, second)
	if compact := x.compactTurn(first); compact.PID != first.pid {
		t.Fatalf("the first session's compact ran in Claude %d, want %d on the first host", compact.PID, first.pid)
	}
	if turns := x.f.Turns(t, second.session.ID); slices.ContainsFunc(turns, func(turn claudehosttest.Turn) bool { return turn.Text == "/compact" }) {
		t.Fatal("the second session compacted before its window")
	}
	x.compactDue(now.Add(46*time.Minute), read, first, second)
	if compact := x.compactTurn(second); compact.PID != second.pid {
		t.Fatalf("the second session's compact ran in Claude %d, want %d on the second host", compact.PID, second.pid)
	}
	testkit.Eventually(t, "both compacts to end", 15*time.Second, func() bool {
		return hostState(t, x.client, first.session.ID) != "busy" && hostState(t, x.client, second.session.ID) == "idle"
	})
	x.d.compactIdle(x.ctx, now.Add(47*time.Minute), read)
	x.gone("both retiring hosts to leave", first.host, first.pid, second.host, second.pid)
}

func TestAHostFromBeforeReleaseIsReleasedOnceAndLeavesAsItAlwaysDid(t *testing.T) {
	f := claudehosttest.New(t, "")
	ctx := context.Background()
	agents := filepath.Join(f.Home, "claude-agents")
	if err := os.WriteFile(agents, []byte("#!/bin/sh\necho '[]'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_BIN", agents)
	old := f.StartProtocol2Host(t, "0.2.50")
	client := claudehost.NewClient(f.Home)
	id := uuid.NewString()
	opened := f.Open(t, client, id)
	if _, err := client.Send(ctx, claudehost.SendRequest{SessionID: id, UUID: uuid.NewString(), Text: "SLOW 60000"}); err != nil {
		t.Fatal(err)
	}
	claude := surfaces.NewClaude(nil, f.Home)
	r := hostRegistry(t)
	if err := r.RegisterSession(hostedSession(t, claude, id)); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, r, []surface.Surface{claude})
	logged := &lockedLog{}
	d.log = log.New(logged, "", 0)
	d.SetClaudeHost(f.Manager(t, "0.2.51"))
	if check := d.checkClaudeHost(ctx, claudeHostCheck{}); check.status != "running" || check.pid == old.PID {
		t.Fatalf("host after the update = %+v", check)
	}
	if retiring := client.Retiring(); len(retiring) != 1 {
		t.Fatalf("retiring hosts = %v, want the protocol 2 host", retiring)
	}

	read := map[string]idleTranscript{}
	d.compactIdle(ctx, time.Now(), read)
	d.compactIdle(ctx, time.Now().Add(time.Minute), read)
	if got := strings.Count(logged.String(), "releasing "); got != 1 {
		t.Fatalf("log = %q, want the session released once", logged.String())
	}
	if strings.Contains(logged.String(), "release "+d.resolveDisplay(id)) {
		t.Fatalf("log = %q, want the old host's invalid answer taken as released", logged.String())
	}
	if !claudehosttest.Alive(opened.PID) {
		t.Fatal("the old host closed a working session")
	}
	if err := client.Interrupt(ctx, id); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the old host to close its idle session itself and leave", 15*time.Second, func() bool {
		return !claudehosttest.Alive(old.PID) && !claudehosttest.Alive(opened.PID)
	})
}
