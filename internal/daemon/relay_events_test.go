package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func (f *daemonSurface) ReadCompletions(ctx context.Context, session *surface.Session, cursor string) (surface.CompletionBatch, error) {
	observation := f.observations[session.ID]
	if f.observeErr != nil {
		return surface.CompletionBatch{}, f.observeErr
	}
	after := ""
	if cursor != "" {
		if err := json.Unmarshal([]byte(cursor), &after); err != nil {
			return surface.CompletionBatch{}, err
		}
	}
	last := after
	var events []surface.Completion
	if observation != nil && observation.CompletedTurnID != "" {
		last = observation.CompletedTurnID
		if cursor != "" && last != after && observation.Reply != nil && observation.Reply.Done && observation.Reply.Error == "" {
			events = append(events, surface.Completion{CompletedAt: time.Now(), ID: last, InputID: observation.InputTurnID, Text: observation.Reply.Text, User: observation.Reply.UserText})
		}
	}
	encoded, _ := json.Marshal(last)
	return surface.CompletionBatch{Cursor: string(encoded), Events: events}, nil
}

func (d *Daemon) fireRelays(from *surface.Session, id string, hops int, text string) {
	cursor, err := d.Registry.RelaySourceCursor(from.ID)
	if err != nil {
		panic(err)
	}
	batch := surface.CompletionBatch{Cursor: "test-producer", Events: []surface.Completion{{ID: id, Text: text, User: fmt.Sprintf("[agenthail relay hops=%d", hops)}}}
	if err := d.Registry.CaptureRelayCompletions(from.ID, cursor, batch); err != nil {
		panic(err)
	}
	routes, err := d.Registry.ListRoutes()
	if err != nil {
		panic(err)
	}
	adapter := d.adapters().ForSurface(from.Surface)
	d.consumeRelays(context.Background(), adapter, from, routes, nil, observeOnce(adapter, from, observeAdapter))
}

func TestDurableRelayClaudeBurstFailureRestartAndOutbox(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	appendTurn := func(id string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Fprintf(f, "{\"type\":\"user\",\"uuid\":\"input-%s\",\"message\":{\"content\":\"request\"}}\n", id)
		fmt.Fprintf(f, "{\"type\":\"assistant\",\"message\":{\"id\":\"%s\",\"stop_reason\":\"end_turn\",\"content\":[{\"type\":\"text\",\"text\":\"answer %s\"}]}}\n", id, id)
		fmt.Fprintln(f, `{"type":"system","subtype":"turn_duration"}`)
	}
	appendTurn("baseline")
	d, r, target, source, to := daemonFixture(t)
	source.Surface = surface.KindClaude
	source.RuntimeKind = surface.RuntimeClaudeCLI
	source.Transcript = path
	if err := r.RegisterSession(source); err != nil {
		t.Fatal(err)
	}
	other := to
	other.ID = "other"
	target.sessions[other.ID] = other
	if err := r.RegisterSession(other); err != nil {
		t.Fatal(err)
	}
	reader := surfaces.NewClaude(nil, t.TempDir())
	if err := r.PrepareRelaySource(context.Background(), reader, &source, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{to.ID, other.ID} {
		if _, err := r.AddRoute(source.ID, id, ".*"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"one", "two", "three"} {
		appendTurn(id)
	}
	db, err := sql.Open("sqlite", r.Path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_target BEFORE INSERT ON message_queue WHEN NEW.session_id='to' BEGIN SELECT RAISE(ABORT,'injected enqueue failure'); END`); err != nil {
		t.Fatal(err)
	}
	d.syncRelays(context.Background(), reader, &source)
	routes, _ := r.ListRoutes()
	if routes[0].EventCursor != 0 || routes[0].PendingCount != 3 || routes[0].LastError == "" || routes[1].FireCount != 3 || r.QueueCount(other.ID) != 3 {
		t.Fatalf("independent progress: %+v", routes)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_target`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := registry.Open(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	restarted := newTestDaemon(t, reopened, []surface.Surface{target})
	restarted.syncRelays(context.Background(), surfaces.NewClaude(nil, t.TempDir()), &source)
	restarted.syncRelays(context.Background(), surfaces.NewClaude(nil, t.TempDir()), &source)
	routes, _ = reopened.ListRoutes()
	if routes[0].FireCount != 3 || routes[0].PendingCount != 0 || routes[1].FireCount != 3 || reopened.QueueCount(to.ID) != 3 {
		t.Fatalf("restart/replay: %+v", routes)
	}
	for i := range 3 {
		target.turnID = fmt.Sprintf("receiver-%d", i)
		restarted.drainMessageQueue(context.Background(), target, &to)
	}
	if len(target.sent) != 3 || reopened.QueueCount(to.ID) != 0 {
		t.Fatalf("receiver=%v queue=%d", target.sent, reopened.QueueCount(to.ID))
	}
	for i, id := range []string{"one", "two", "three"} {
		if !strings.Contains(target.sent[i], "answer "+id) {
			t.Fatalf("out-of-order handoff: %v", target.sent)
		}
	}
}

type goalRelaySurface struct {
	*daemonSurface
	goal    *surface.GoalState
	goalErr error
}

func (f *goalRelaySurface) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	return f.goal, f.goalErr
}

func TestGoalRelayWaitsForExplicitCompletionAndReleasesLatestOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, base, from, to := daemonFixture(t)
	base.caps = surface.Capabilities{Goal: true, Send: true}
	fake := &goalRelaySurface{daemonSurface: base, goal: &surface.GoalState{Objective: "finish", Status: surface.GoalStatusActive, CreatedAt: time.Now().Add(-time.Minute)}}
	d.Surfaces = []surface.Surface{fake}
	if err := r.PrepareRelaySource(context.Background(), fake, &from, registry.HandoffGoalCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRelayRoute(from.ID, to.ID, ".*", false, registry.HandoffGoalCompleted); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"progress", "final"} {
		base.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: id, Reply: &surface.ReplyResult{Text: id, Done: true}}
		d.syncRelays(context.Background(), fake, &from)
	}
	for _, status := range []string{surface.GoalStatusPaused, surface.GoalStatusBlocked, surface.GoalStatusBudgetLimited, "unknown"} {
		fake.goal.Status = status
		d.syncRelays(context.Background(), fake, &from)
		if r.QueueCount(to.ID) != 0 {
			t.Fatalf("status %s released a goal", status)
		}
	}
	fake.goal.Status = surface.GoalStatusComplete
	fake.goalErr = fmt.Errorf("goal lookup offline")
	d.syncRelays(context.Background(), fake, &from)
	if r.QueueCount(to.ID) != 0 {
		t.Fatal("unknown goal released")
	}
	fake.goalErr = nil
	base.observations[from.ID].ActiveTurnID = "still-working"
	d.syncRelays(context.Background(), fake, &from)
	if r.QueueCount(to.ID) != 0 {
		t.Fatal("goal released before final turn")
	}
	base.observations[from.ID].ActiveTurnID = ""
	restarted := newTestDaemon(t, r, []surface.Surface{fake})
	restarted.syncRelays(context.Background(), fake, &from)
	restarted.syncRelays(context.Background(), fake, &from)
	if r.QueueCount(to.ID) != 1 {
		t.Fatalf("goal handoffs=%d", r.QueueCount(to.ID))
	}
	item, err := r.QueueItem(1, nil)
	if err != nil || !strings.HasSuffix(item.Message, "final") {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	base.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "later", Reply: &surface.ReplyResult{Text: "later", Done: true}}
	restarted.syncRelays(context.Background(), fake, &from)
	if r.QueueCount(to.ID) != 1 {
		t.Fatal("same completed goal fired twice")
	}
	routes, _ := r.ListRoutes()
	if routes[0].PendingCount != 1 || routes[0].LastError == "" {
		t.Fatalf("goal waiting status=%+v", routes[0])
	}
}

func TestDashboardRelayConditionRoundTripAndUnsupportedGoal(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	for _, handoff := range []string{"goal_completed", "invalid", "turn_finished"} {
		request := httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(fmt.Sprintf(`{"action":"relay-add","fromId":"from","toId":"to","handoff":%q}`, handoff)))
		response := httptest.NewRecorder()
		d.dashboardActionHandler(response, request)
		want := http.StatusBadRequest
		if handoff == "turn_finished" {
			want = http.StatusOK
		}
		if response.Code != want {
			t.Fatalf("handoff=%s status=%d body=%s", handoff, response.Code, response.Body.String())
		}
	}
	fake.caps = surface.Capabilities{Goal: true}
	response := httptest.NewRecorder()
	d.dashboardActionHandler(response, httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"relay-add","fromId":"from","toId":"to","handoff":"goal_completed","once":true}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d %s", response.Code, response.Body.String())
	}
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 2 || routes[1].Handoff != registry.HandoffGoalCompleted || !routes[1].Once {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	state, err := d.dashboardState(context.Background())
	if err != nil || len(state.Relays) != 2 || state.Relays[1].Handoff != registry.HandoffGoalCompleted {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}

func TestGoalRelayRequiresCompletionWithinCurrentGoal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, base, from, to := daemonFixture(t)
	base.caps = surface.Capabilities{Goal: true, Send: true}
	fake := &goalRelaySurface{daemonSurface: base, goal: &surface.GoalState{Objective: "finish", Status: surface.GoalStatusComplete, CreatedAt: time.Now().Add(-time.Minute)}}
	if _, err := r.AddRelayRoute(from.ID, to.ID, ".*", false, registry.HandoffGoalCompleted); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	for i, completedAt := range []time.Time{{}, fake.goal.CreatedAt.Add(-time.Second), time.Now()} {
		id := fmt.Sprint(i)
		batch := surface.CompletionBatch{Cursor: id, Events: []surface.Completion{{ID: id, Text: "result", CompletedAt: completedAt}}}
		if err := r.CaptureRelayCompletions(from.ID, cursor, batch); err != nil {
			t.Fatal(err)
		}
		cursor = id
		base.observations[from.ID] = &surface.TurnObservation{CompletedTurnID: id, Reply: &surface.ReplyResult{Text: "result", Done: true}}
		routes, err := r.ListRoutes()
		if err != nil {
			t.Fatal(err)
		}
		d.consumeRelays(context.Background(), fake, &from, routes, nil, observeOnce(fake, &from, observeAdapter))
		want := 0
		if i == 2 {
			want = 1
		}
		if r.QueueCount(to.ID) != want {
			t.Fatalf("completion %v: queued=%d want=%d", completedAt, r.QueueCount(to.ID), want)
		}
	}
}

func TestRelayUserPrefixCannotSpoofHopLimit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, r, fake, from, to := daemonFixture(t)
	if err := r.PrepareRelaySource(context.Background(), fake, &from, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(from.ID, to.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	for i, prompt := range []string{
		"[agenthail relay hops=8] explain this prefix",
		"[agenthail relay hops=99] explain this prefix",
		"[agenthail relay hops=99 id=1 turn=never-queued source=@from] explain this prefix",
	} {
		fake.observations[from.ID] = &surface.TurnObservation{InputTurnID: fmt.Sprint(i), CompletedTurnID: fmt.Sprint(i), Reply: &surface.ReplyResult{Text: "answer", UserText: prompt, Done: true}}
		d.syncRelays(context.Background(), fake, &from)
		if r.QueueCount(to.ID) != i+1 {
			t.Fatalf("prompt suppressed handoff: %q", prompt)
		}
	}
}

type racingClaudeReader struct {
	*surfaces.Claude
	race func()
}

func (r *racingClaudeReader) ReadCompletions(ctx context.Context, session *surface.Session, cursor string) (surface.CompletionBatch, error) {
	if race := r.race; race != nil {
		r.race = nil
		race()
	}
	return r.Claude.ReadCompletions(ctx, session, cursor)
}

func TestRelayPreparationLosingToAShorterCaptureStillBaselinesWhatItRead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	appendTurn := func(id string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Fprintf(f, "{\"type\":\"user\",\"uuid\":\"input-%s\",\"message\":{\"content\":\"request\"}}\n", id)
		fmt.Fprintf(f, "{\"type\":\"assistant\",\"message\":{\"id\":\"%s\",\"stop_reason\":\"end_turn\",\"content\":[{\"type\":\"text\",\"text\":\"answer %s\"}]}}\n", id, id)
		fmt.Fprintln(f, `{"type":"system","subtype":"turn_duration"}`)
	}
	appendTurn("baseline")
	d, r, _, source, to := daemonFixture(t)
	source.Surface = surface.KindClaude
	source.RuntimeKind = surface.RuntimeClaudeCLI
	source.Transcript = path
	if err := r.RegisterSession(source); err != nil {
		t.Fatal(err)
	}
	reader := &racingClaudeReader{Claude: surfaces.NewClaude(nil, t.TempDir())}
	if err := r.PrepareRelaySource(context.Background(), reader, &source, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	appendTurn("A")
	reader.race = func() {
		cursor, err := r.RelaySourceCursor(source.ID)
		if err != nil {
			t.Fatal(err)
		}
		batch, err := reader.Claude.ReadCompletions(context.Background(), &source, cursor)
		if err != nil || len(batch.Events) != 1 {
			t.Fatalf("scan batch=%+v err=%v", batch, err)
		}
		appendTurn("B")
		if err := r.CaptureRelayCompletions(source.ID, cursor, batch); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.PrepareRelaySource(context.Background(), reader, &source, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(source.ID, to.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	appendTurn("C")
	d.syncRelays(context.Background(), reader, &source)
	routes, _ := r.ListRoutes()
	if len(routes) != 1 || routes[0].FireCount != 1 || routes[0].PendingCount != 0 || routes[0].LastError != "" || r.QueueCount(to.ID) != 1 {
		t.Fatalf("routes=%+v queued=%d", routes, r.QueueCount(to.ID))
	}
	items, err := r.ListQueue(false, nil)
	if err != nil || len(items) != 1 || !strings.Contains(items[0].Message, "answer C") {
		t.Fatalf("queue=%+v err=%v", items, err)
	}
}

func TestDurableRelayClaudeRelaysReplyTextOnceAfterItsLastContentBlock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	write := func(records ...string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		for _, record := range records {
			fmt.Fprintln(f, record)
		}
	}
	prompt := func(id string) string {
		return fmt.Sprintf(`{"type":"user","uuid":"input-%s","message":{"content":"request %s"}}`, id, id)
	}
	thinking := func(id string) string {
		return fmt.Sprintf(`{"type":"assistant","message":{"id":"%s","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"","signature":"sig"}]}}`, id)
	}
	text := func(id string) string {
		return fmt.Sprintf(`{"type":"assistant","message":{"id":"%s","stop_reason":"end_turn","content":[{"type":"text","text":"answer %s"}]}}`, id, id)
	}
	const hook = `{"type":"system","subtype":"stop_hook_summary"}`
	const duration = `{"type":"system","subtype":"turn_duration"}`
	write(prompt("baseline"), text("baseline"), duration)
	d, r, _, source, to := daemonFixture(t)
	source.Surface = surface.KindClaude
	source.RuntimeKind = surface.RuntimeClaudeCLI
	source.Transcript = path
	if err := r.RegisterSession(source); err != nil {
		t.Fatal(err)
	}
	if err := r.PrepareRelaySource(context.Background(), surfaces.NewClaude(nil, t.TempDir()), &source, registry.HandoffTurnFinished); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute(source.ID, to.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	fires := func() int64 {
		routes, err := r.ListRoutes()
		if err != nil || len(routes) != 1 || routes[0].LastError != "" {
			t.Fatalf("routes=%+v err=%v", routes, err)
		}
		return routes[0].FireCount
	}
	write(prompt("one"), thinking("one"), text("one"), hook, duration)
	d.syncRelays(context.Background(), surfaces.NewClaude(nil, t.TempDir()), &source)
	if fires() != 1 || r.QueueCount(to.ID) != 1 {
		t.Fatalf("thinking and text in one read: fires=%d queued=%d", fires(), r.QueueCount(to.ID))
	}
	write(prompt("two"), thinking("two"))
	d.syncRelays(context.Background(), surfaces.NewClaude(nil, t.TempDir()), &source)
	if fires() != 1 || r.QueueCount(to.ID) != 1 {
		t.Fatalf("closing turn relayed before its text: fires=%d queued=%d", fires(), r.QueueCount(to.ID))
	}
	write(text("two"), duration)
	d.syncRelays(context.Background(), surfaces.NewClaude(nil, t.TempDir()), &source)
	d.syncRelays(context.Background(), surfaces.NewClaude(nil, t.TempDir()), &source)
	if fires() != 2 || r.QueueCount(to.ID) != 2 {
		t.Fatalf("split turn: fires=%d queued=%d", fires(), r.QueueCount(to.ID))
	}
	items, err := r.ListQueue(false, nil)
	if err != nil || len(items) != 2 || !strings.Contains(items[0].Message, "answer one") || !strings.Contains(items[1].Message, "answer two") {
		t.Fatalf("queue=%+v err=%v", items, err)
	}
}

func TestTurnFinishedRelayWaitsForBackgroundWork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	shell := &surface.BackgroundTasks{Running: true, Source: surface.BackgroundSourceHost, Tasks: []surface.BackgroundTask{{ID: "b1", Kind: surface.BackgroundShell}}}
	monitor := &surface.BackgroundTasks{Source: surface.BackgroundSourceHost, Tasks: []surface.BackgroundTask{{ID: "m1", Kind: surface.BackgroundMonitor, Ambient: true}}}
	setup := func(t *testing.T, once bool) (*Daemon, *registry.Registry, *daemonSurface, surface.Session, surface.Session) {
		d, r, fake, from, to := daemonFixture(t)
		if err := r.PrepareRelaySource(context.Background(), fake, &from, registry.HandoffTurnFinished); err != nil {
			t.Fatal(err)
		}
		if _, err := r.AddRelayRoute(from.ID, to.ID, ".*", once, registry.HandoffTurnFinished); err != nil {
			t.Fatal(err)
		}
		return d, r, fake, from, to
	}
	finish := func(d *Daemon, fake *daemonSurface, from surface.Session, id string, status surface.SessionStatus, background *surface.BackgroundTasks) {
		fake.observations[from.ID] = &surface.TurnObservation{Status: status, CompletedTurnID: id, Reply: &surface.ReplyResult{Text: id, Done: true}, BackgroundTasks: background}
		d.syncRelays(context.Background(), fake, &from)
	}
	queued := func(t *testing.T, r *registry.Registry, to surface.Session) []string {
		t.Helper()
		var messages []string
		for id := int64(1); id <= int64(r.QueueCount(to.ID)); id++ {
			item, err := r.QueueItem(id, nil)
			if err != nil {
				t.Fatal(err)
			}
			messages = append(messages, item.Message[strings.LastIndex(item.Message, "] ")+2:])
		}
		return messages
	}

	t.Run("held while a shell runs, then every turn delivers", func(t *testing.T) {
		d, r, fake, from, to := setup(t, false)
		finish(d, fake, from, "started", surface.StatusIdle, shell)
		finish(d, fake, from, "checked", surface.StatusIdle, shell)
		if got := queued(t, r, to); len(got) != 0 {
			t.Fatalf("delivered %v while background work ran", got)
		}
		if routes, _ := r.ListRoutes(); routes[0].PendingCount != 2 || !strings.Contains(routes[0].LastError, "background") {
			t.Fatalf("route=%+v", routes[0])
		}
		finish(d, fake, from, "done", surface.StatusIdle, monitor)
		if got := queued(t, r, to); strings.Join(got, ",") != "started,checked,done" {
			t.Fatalf("delivered %v", got)
		}
		if routes, _ := r.ListRoutes(); routes[0].LastError != "" {
			t.Fatalf("route=%+v", routes[0])
		}
	})

	t.Run("an ambient monitor alone does not hold", func(t *testing.T) {
		d, r, fake, from, to := setup(t, false)
		finish(d, fake, from, "watching", surface.StatusIdle, monitor)
		if got := queued(t, r, to); strings.Join(got, ",") != "watching" {
			t.Fatalf("delivered %v", got)
		}
	})

	t.Run("a session that closes with work pending delivers", func(t *testing.T) {
		d, r, fake, from, to := setup(t, false)
		finish(d, fake, from, "started", surface.StatusIdle, shell)
		finish(d, fake, from, "started", surface.StatusOffline, shell)
		if got := queued(t, r, to); strings.Join(got, ",") != "started" {
			t.Fatalf("delivered %v", got)
		}
	})

	t.Run("once delivers the turn that finished the work", func(t *testing.T) {
		d, r, fake, from, to := setup(t, true)
		finish(d, fake, from, "started", surface.StatusIdle, shell)
		finish(d, fake, from, "done", surface.StatusIdle, nil)
		finish(d, fake, from, "later", surface.StatusIdle, nil)
		if got := queued(t, r, to); strings.Join(got, ",") != "done" {
			t.Fatalf("delivered %v", got)
		}
	})

	t.Run("unreadable state falls back to the last stored state", func(t *testing.T) {
		d, r, fake, from, to := setup(t, false)
		finish(d, fake, from, "started", surface.StatusIdle, shell)
		fake.observeErr = fmt.Errorf("source unreachable")
		from.BackgroundTasks = shell
		d.syncRelays(context.Background(), fake, &from)
		if got := queued(t, r, to); len(got) != 0 {
			t.Fatalf("delivered %v while the stored state shows background work", got)
		}
		from.BackgroundTasks = nil
		d.syncRelays(context.Background(), fake, &from)
		if got := queued(t, r, to); strings.Join(got, ",") != "started" {
			t.Fatalf("delivered %v", got)
		}
	})

	t.Run("a scan observes the source once however many relays wait", func(t *testing.T) {
		d, r, fake, from, to := setup(t, false)
		if _, err := r.AddRelayRoute(from.ID, to.ID, ".*", false, registry.HandoffTurnFinished); err != nil {
			t.Fatal(err)
		}
		fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle}
		d.syncRelays(context.Background(), fake, &from)
		if calls := fake.observeCalls.Load(); calls != 0 {
			t.Fatalf("relays observed %d times with nothing pending", calls)
		}
		fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "started", Reply: &surface.ReplyResult{Text: "started", Done: true}, BackgroundTasks: shell}
		d.observeSession(context.Background(), fake, &from)
		if calls := fake.observeCalls.Load(); calls != 1 {
			t.Fatalf("one scan with two waiting relays observed %d times", calls)
		}
		if got := queued(t, r, to); len(got) != 0 {
			t.Fatalf("delivered %v while background work ran", got)
		}
	})
}
