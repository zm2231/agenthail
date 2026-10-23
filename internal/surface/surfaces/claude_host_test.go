package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/claudepeer"
	"github.com/zm2231/agenthail/internal/installdir"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func hostedClaude(t *testing.T) (*Claude, *claudehosttest.Fixture) {
	t.Helper()
	fixture := claudehosttest.New(t, "")
	// Resuming reads Claude's background job catalog through the claude binary.
	claudetest.New(t)
	return NewClaude(nil, fixture.Home), fixture
}

func listedSession(t *testing.T, c *Claude, id string) *surface.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var found *surface.Session
	claudehosttest.WaitFor(t, "the session to be listed on the host runtime", func() bool {
		c.host.listed = time.Time{}
		sessions, err := c.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i := range sessions {
			if sessions[i].ID == id && sessions[i].RuntimeKind == surface.RuntimeClaudeHost {
				found = &sessions[i]
				return true
			}
		}
		return false
	})
	return found
}

func origin(t *testing.T, turn claudehosttest.Turn) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(turn.Origin, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAHostSessionIsListedOnTheHostRuntimeWithItsNativeSocket(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	opened := fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	if sess.PID != opened.PID || sess.Bridge != "" || sess.Transport != "uds" {
		t.Fatalf("listed %+v, want pid %d, the native socket and no bridge", sess, opened.PID)
	}
	if got := c.Route(sess, surface.AuthorPerson); got != surface.RouteHumanTurn {
		t.Fatalf("person route = %v, want a human turn", got)
	}
	if got := c.Route(sess, ""); got != surface.RoutePeer {
		t.Fatalf("agent route = %v, want the native socket", got)
	}
	caps := surface.EffectiveCapabilities(c, sess)
	if !caps.Steer || !caps.Interrupt || caps.Compact || caps.Commands || !caps.Model || !caps.Respond || !caps.PermissionMode {
		t.Fatalf("capabilities = %+v, want every host control and no compact or Remote Control commands", caps.Capabilities)
	}
}

func TestAPersonsTurnToAHostSessionCarriesTheIntentUUIDAndRunsOnce(t *testing.T) {
	c, fixture := hostedClaude(t)
	c.localOnly = true
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	message := uuid.NewString()
	ctx := surface.WithMessageUUID(context.Background(), message)
	for range 2 {
		result, err := c.SendHumanTurn(ctx, sess, "hello from a person", nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.UUID != message || !result.Accepted {
			t.Fatalf("result = %+v, want accepted with the intent uuid", result)
		}
	}
	claudehosttest.WaitFor(t, "the turn", func() bool { return len(fixture.Turns(t, id)) == 1 })
	time.Sleep(200 * time.Millisecond)
	turns := fixture.Turns(t, id)
	if len(turns) != 1 || turns[0].UUID != message || turns[0].Text != "hello from a person" || origin(t, turns[0])["kind"] != "human" {
		t.Fatalf("turns = %+v, want one human turn with the intent uuid", turns)
	}
}

func TestAnAgentMessageToAHostSessionArrivesOverItsNativeSocket(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	sender, target := uuid.NewString(), uuid.NewString()
	fixture.Open(t, client, sender)
	fixture.Open(t, client, target)
	senderSession := listedSession(t, c, sender)
	sess := listedSession(t, c, target)
	ctx := surface.WithSourceSessionID(context.Background(), sender)
	if _, err := c.Send(ctx, sess, "/compact"); !surface.IsDeliveryTerminal(err) {
		t.Fatalf("slash command over the socket = %v, want refused", err)
	}
	if _, err := c.Send(ctx, sess, "from another session"); err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "the peer turn", func() bool { return len(fixture.Turns(t, target)) == 1 })
	turn := fixture.Turns(t, target)[0]
	senderSocket := "uds:/tmp/cc-socks/" + strconv.Itoa(senderSession.PID) + ".sock"
	if origin(t, turn)["kind"] != "peer" || origin(t, turn)["from"] != senderSocket || !strings.Contains(turn.Text, `from="`+senderSocket+`"`) || !strings.Contains(turn.Text, "from another session") {
		t.Fatalf("turn = %+v, want a peer message from %s", turn, senderSocket)
	}
}

// Claude holds a peer message for a session that runs without permission
// prompts unless the sender states that it runs the same way, and a hosted
// session has no prompt to release it.
func TestAnAgentMessageBetweenSessionsThatBypassPromptsRunsATurn(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	sender, target := uuid.NewString(), uuid.NewString()
	fixture.OpenInMode(t, client, sender, "bypassPermissions")
	fixture.OpenInMode(t, client, target, "bypassPermissions")
	listedSession(t, c, sender)
	sess := listedSession(t, c, target)
	if _, err := c.Send(surface.WithSourceSessionID(context.Background(), sender), sess, "from a bypassing session"); err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "the peer turn", func() bool { return len(fixture.Turns(t, target)) == 1 })
	if turn := fixture.Turns(t, target)[0]; !strings.Contains(turn.Text, `from-mode="bypass"`) {
		t.Fatalf("turn = %+v, want the sender's permission class stated", turn)
	}
}

func TestSteerAndInterruptAHostSession(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	ctx := context.Background()
	if _, err := c.SendHumanTurn(surface.WithMessageUUID(ctx, uuid.NewString()), sess, "SLOW 30000", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := c.Steer(ctx, sess, "steered"); err != nil {
		t.Fatal(err)
	}
	if err := c.Interrupt(ctx, sess); err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "both turns", func() bool { return len(fixture.Turns(t, id)) == 2 })
	turns := fixture.Turns(t, id)
	if turns[0].Reply != "interrupted" || turns[1].Text != "steered" || turns[1].Priority != "now" {
		t.Fatalf("turns = %+v, want the slow turn interrupted and the steer sent with priority now", turns)
	}
}

func TestAPersonsTurnWaitsForAnOrphanedSessionAndThenResumesItOnce(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	opened := fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	if _, err := c.SendHumanTurn(surface.WithMessageUUID(context.Background(), uuid.NewString()), sess, "SLOW 3000", nil); err != nil {
		t.Fatal(err)
	}
	fixture.KillHost(t)
	if !claudehosttest.Alive(opened.PID) {
		t.Fatal("the session's CLI outlives the host while its turn runs")
	}
	held := func(when string) {
		t.Helper()
		orphaned := listedSession(t, c, id)
		if orphaned.PID != opened.PID || c.Route(orphaned, surface.AuthorPerson) != surface.RouteHumanTurn {
			t.Fatalf("%s: session = %+v route = %v, want the orphaned process to keep the host route", when, orphaned, c.Route(orphaned, surface.AuthorPerson))
		}
		if _, err := c.SendHumanTurn(surface.WithMessageUUID(context.Background(), uuid.NewString()), orphaned, "early", nil); !surface.IsDeliveryHeld(err) {
			t.Fatalf("%s: send = %v, want held", when, err)
		}
	}
	held("while the host is down")
	fixture.Start(t)
	held("while a new host runs and the orphan still holds the session")
	claudehosttest.WaitFor(t, "the orphaned CLI to finish its turn and exit", func() bool { return !claudehosttest.Alive(opened.PID) })
	c.host.forget()
	if _, err := c.SendHumanTurn(surface.WithMessageUUID(context.Background(), uuid.NewString()), sess, "after", nil); err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "the resumed turn", func() bool { return len(fixture.Turns(t, id)) >= 2 })
	var texts []string
	for _, turn := range fixture.Turns(t, id) {
		texts = append(texts, turn.Text)
	}
	if strings.Join(texts, ",") != "SLOW 3000,after" || len(fixture.Spawns(t, id)) != 2 {
		t.Fatalf("turns = %q spawns = %d, want the held messages undelivered and one resume", texts, len(fixture.Spawns(t, id)))
	}
}

func TestHostErrorsMapToDeliveryOutcomes(t *testing.T) {
	host := func(code string) error { return &claudehost.Error{Code: code, Message: code} }
	for _, tc := range []struct {
		name string
		err  error
		want func(error) bool
	}{
		{"not running", claudehost.ErrNotRunning, surface.IsDeliveryHeld},
		{"held", host("held"), surface.IsDeliveryHeld},
		{"draining", host("draining"), surface.IsDeliveryHeld},
		{"not open", host("not_open"), func(err error) bool { return surface.IsDeliveryUnavailable(err) && !surface.IsDeliveryHeld(err) }},
		{"unknown", host("unknown"), surface.IsDeliveryOutcomeUnknown},
		{"no answer", &claudehost.OutcomeUnknownError{Err: context.DeadlineExceeded}, surface.IsDeliveryOutcomeUnknown},
		{"invalid", host("invalid"), func(err error) bool {
			return surface.DeliveryTerminalReason(err) == surface.DeliveryInvalidRequest
		}},
	} {
		if got := claudeHostDeliveryError(tc.err); !tc.want(got) {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
	if got := claudeHostResumeError(fixtureConversation, host("refused")); !surface.IsDeliveryRefused(got) {
		t.Errorf("a resume Claude refused: %v", got)
	}
	if got := claudeHostResumeError(fixtureConversation, host("open_failed")); !surface.IsDeliveryUnavailable(got) || surface.IsDeliveryRefused(got) || surface.IsDeliveryHeld(got) {
		t.Errorf("a resume Claude did not start: %v, want a retried unavailable delivery", got)
	}
}

func TestAHostSessionIsOpenWhileTheHostListsIt(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	cli := surface.Session{ID: uuid.NewString(), Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: sess.PID}
	open := c.OpenSessions(context.Background(), []surface.Session{*sess, cli})
	if !open[id] || open[cli.ID] {
		t.Fatalf("open = %v, want the host session and not a CLI row on its pid", open)
	}
	if err := client.Close(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	claudehosttest.WaitFor(t, "the host to stop listing the session", func() bool {
		c.host.listed = time.Time{}
		return !c.OpenSessions(context.Background(), []surface.Session{*sess})[id]
	})
}

func TestWithoutAHostOnlyARecordedProcessThatStillRunsCountsAsHosted(t *testing.T) {
	home := t.TempDir()
	c := NewClaude(nil, home)
	live := os.Getpid()
	started, err := claudepeer.ProcessStart(live)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"sessions": map[string]any{
		"running": map[string]any{"pid": live, "started": started},
		"reused":  map[string]any{"pid": live, "started": "Thu Jan  1 00:00:00 1970"},
		"exited":  map[string]any{"pid": 999999, "started": started},
		"untimed": map[string]any{"pid": live, "started": ""},
	}}
	data, _ := json.Marshal(state)
	if err := os.MkdirAll(claudehost.StateDir(home), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudehost.StateDir(home), "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	for conversation, want := range map[string]bool{"running": true, "reused": false, "exited": false, "untimed": false} {
		c.host.forget()
		if got := c.host.runs(context.Background(), live, conversation) || c.host.runs(context.Background(), 999999, conversation); got != want {
			t.Fatalf("%s hosted=%v, want %v", conversation, got, want)
		}
	}
}

func TestTheClaudeHostInheritsAConfiguredPrimaryConfigDirAndDropsABlankOne(t *testing.T) {
	fixture := claudetest.New(t)
	script := filepath.Join(fixture.Home, "host.mjs")
	if err := os.WriteFile(script, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_HOST", script)
	installNode(t, "#!/bin/sh\n")
	configured := filepath.Join(fixture.Home, "claude-main")
	for value, want := range map[string][]string{"": nil, "  ": nil, configured: {"CLAUDE_CONFIG_DIR=" + configured}} {
		t.Setenv("CLAUDE_CONFIG_DIR", value)
		launch, err := ClaudeHostLaunch(fixture.Home, "test")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, entry := range launch.Env {
			if strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR=") {
				got = append(got, entry)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("CLAUDE_CONFIG_DIR=%q: host env has %q, want %q", value, got, want)
		}
	}
}

func TestANewHostSessionStreamsItsFirstReplyBeforeItsTranscriptExists(t *testing.T) {
	cases := []struct {
		name        string
		folder      func(cwd string) string
		lookupTicks int
	}{
		// The folder Claude usually picks is checked on every tick; the full
		// lookup is pushed out of reach so only that check can find it.
		{"in the folder Claude usually picks", claudeProjectDirName, 1 << 20},
		// Claude names the folder itself, so one named some other way is
		// found by the lookup across every project folder.
		{"in a folder named some other way", func(string) string { return "a-folder-claude-named" }, claudeHostTranscriptLookupTicks},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func(ticks int) { claudeHostTranscriptLookupTicks = ticks }(claudeHostTranscriptLookupTicks)
			claudeHostTranscriptLookupTicks = tc.lookupTicks
			c, fixture := hostedClaude(t)
			client := fixture.Start(t)
			id := uuid.NewString()
			fixture.Open(t, client, id)
			sess := listedSession(t, c, id)
			if sess.Transcript != "" || !surface.EffectiveCapabilities(c, sess).Stream {
				t.Fatalf("a new host session offers its stream before its transcript exists: transcript %q, stream %v", sess.Transcript, surface.EffectiveCapabilities(c, sess).Stream)
			}
			path := filepath.Join(fixture.Home, ".claude", "projects", tc.folder(sess.Cwd), id+".jsonl")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			got := make(chan string, 16)
			streamed := make(chan error, 1)
			go func() {
				streamed <- c.Stream(ctx, sess, "", func(event surface.StreamEvent) { got <- event.Text }, 15*time.Second)
			}()
			time.Sleep(300 * time.Millisecond)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			transcript := `{"type":"user","uuid":"u1","message":{"content":"say hello"}}` + "\n" +
				`{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"the first reply"}]}}` + "\n"
			if err := os.WriteFile(path, []byte(transcript), 0600); err != nil {
				t.Fatal(err)
			}
			for {
				select {
				case text := <-got:
					if text == "the first reply" {
						cancel()
						<-streamed
						return
					}
				case err := <-streamed:
					t.Fatalf("the stream ended before the first reply: %v", err)
				case <-ctx.Done():
					t.Fatal("the first reply written after the stream opened never arrived")
				}
			}
		})
	}
}

func TestAStreamWhoseSourceSawNoTranscriptReadsOneCreatedBeforeItStartedFromTheStart(t *testing.T) {
	c, fixture := hostedClaude(t)
	client := fixture.Start(t)
	id := uuid.NewString()
	fixture.Open(t, client, id)
	sess := listedSession(t, c, id)
	// The source seeded before the first turn, and the transcript appeared
	// before its stream opened.
	sess.TranscriptFromStart = true
	path := filepath.Join(fixture.Home, ".claude", "projects", "a-folder-claude-named", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","uuid":"u1","message":{"content":"say hello"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","message":{"id":"m1","stop_reason":"end_turn","content":[{"type":"text","text":"the first reply"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got := make(chan string, 16)
	streamed := make(chan error, 1)
	go func() {
		streamed <- c.Stream(ctx, sess, "", func(event surface.StreamEvent) { got <- event.Text }, 8*time.Second)
	}()
	for {
		select {
		case text := <-got:
			if text == "the first reply" {
				cancel()
				<-streamed
				return
			}
		case err := <-streamed:
			t.Fatalf("the stream ended before the first reply: %v", err)
		case <-ctx.Done():
			t.Fatal("a reply written before the stream opened, after the seed found nothing, was skipped")
		}
	}
}

// installNode stands script in for the Node runtime of an install at a
// temporary root, which the daemon runs in place of any node on PATH.
func installNode(t *testing.T, script string) string {
	t.Helper()
	root := t.TempDir()
	node := installdir.Node(root)
	if err := os.MkdirAll(filepath.Dir(node), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	previous := installRoot
	installRoot = func() (string, error) { return root, nil }
	t.Cleanup(func() { installRoot = previous })
	return node
}

func TestTheClaudeHostRunsTheNodeAgenthailShips(t *testing.T) {
	fixture := claudetest.New(t)
	script := filepath.Join(fixture.Home, "host.mjs")
	if err := os.WriteFile(script, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CLAUDE_HOST", script)
	onPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(onPath, "node"), []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", onPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	node := installNode(t, "#!/bin/sh\n")
	launch, err := ClaudeHostLaunch(fixture.Home, "test")
	if err != nil || launch.Node != node {
		t.Fatalf("node=%q err=%v, want the shipped %q", launch.Node, err, node)
	}
	if err := os.Remove(node); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaudeHostLaunch(fixture.Home, "test"); err == nil || !strings.Contains(err.Error(), node) || !strings.Contains(err.Error(), "reinstall Agenthail") {
		t.Fatalf("a missing shipped Node fell back or was not explained: %v", err)
	}
}
