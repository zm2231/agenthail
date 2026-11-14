package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type peerRoutedSurface struct {
	*cliSurface
}

func (peerRoutedSurface) Route(*surface.Session, surface.Author) surface.Route {
	return surface.RoutePeer
}

type openSessionStream struct {
	events []sessionstream.Event
}

func (r openSessionStream) OpenSessionStream(context.Context, *surface.Session, uint64) (sessionstream.Subscription, error) {
	events := make(chan sessionstream.Event, len(r.events))
	for _, event := range r.events {
		events <- event
	}
	return sessionstream.Subscription{Events: events, Cancel: func() {}}, nil
}

func peerTargetApp(t *testing.T, runtime surface.RuntimeKind, events []sessionstream.Event) (*App, *cliSurface) {
	t.Helper()
	session := surface.Session{ID: "s", Surface: surface.KindClaude, RuntimeKind: runtime, Transport: "uds"}
	fake := &cliSurface{
		kind:       surface.KindClaude,
		sessions:   map[string]surface.Session{"s": session},
		caps:       surface.Capabilities{Send: true, Stream: true},
		sendResult: &surface.SendResult{UUID: "msg-1", Accepted: true},
	}
	app, _ := cliFixture(t, fake)
	app.Surfaces[0].Surface = peerRoutedSurface{fake}
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) { return testSessionPageReader{}, nil }
	app.daemonSessionStreamReader = func() (sessionStreamReader, error) { return openSessionStream{events: events}, nil }
	return app, fake
}

func TestAReplySendToAnIdleHostedClaudeFollowsTheTurnItsMessageStarted(t *testing.T) {
	app, _ := peerTargetApp(t, surface.RuntimeClaudeHost, []sessionstream.Event{
		{Seq: 1, ItemID: "earlier", Version: 1, Kind: "text", Role: "assistant", TurnID: "earlier-turn", Body: "unrelated"},
		{Seq: 2, ItemID: "earlier-done", Kind: "done", TurnID: "earlier-turn"},
		{Seq: 3, ItemID: "prompt", Version: 1, Kind: "message", Role: "peer", TurnID: "record-1", MessageID: "msg-1", TurnStart: true, Body: "hello"},
		{Seq: 4, ItemID: "answer", Version: 1, Kind: "text", Role: "assistant", TurnID: "record-1", Body: "the answer"},
		{Seq: 5, ItemID: "done", Kind: "done", TurnID: "record-1"},
	})
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "claude:s", "hello", "--reply"}) })
	if err != nil || !strings.Contains(output, "the answer\nsent (message msg-1, turn record-1)") || strings.Contains(output, "unrelated") || strings.Contains(output, joinedRunningTurnNote) {
		t.Fatalf("output=%q err=%v, want only the reply of the turn the message started", output, err)
	}
}

func TestAReplySendToABusyHostedClaudeSaysItJoinedTheRunningTurn(t *testing.T) {
	events := []sessionstream.Event{
		{Seq: 1, ItemID: "before", Version: 1, Kind: "text", Role: "assistant", TurnID: "running", Body: "before the message"},
		{Seq: 2, ItemID: "absorbed", Version: 1, Kind: "message", Role: "peer", TurnID: "running", MessageID: "msg-1", Body: "hello"},
		{Seq: 3, ItemID: "after", Version: 1, Kind: "text", Role: "assistant", TurnID: "running", Body: "after the message"},
		{Seq: 4, ItemID: "done", Kind: "done", TurnID: "running"},
	}
	for _, mode := range []string{"--reply", "--stream"} {
		t.Run(mode, func(t *testing.T) {
			app, _ := peerTargetApp(t, surface.RuntimeClaudeHost, events)
			output, err := captureStdout(t, func() error { return app.Run([]string{"send", "claude:s", "hello", mode}) })
			if err != nil || !strings.HasPrefix(output, joinedRunningTurnNote+"\n") || !strings.Contains(output, "after the message") || strings.Contains(output, "before the message") {
				t.Fatalf("output=%q err=%v, want the note and the running turn's output from the message on", output, err)
			}
		})
	}
	app, _ := peerTargetApp(t, surface.RuntimeClaudeHost, events)
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "claude:s", "hello", "--reply", "--json"}) })
	var result struct {
		Joined bool                `json:"joinedRunningTurn"`
		Reply  surface.ReplyResult `json:"reply"`
	}
	if err != nil || json.Unmarshal([]byte(output), &result) != nil || !result.Joined || result.Reply.Text != "after the message" {
		t.Fatalf("output=%q err=%v, want joinedRunningTurn and the running turn's reply", output, err)
	}
}

func TestAReplySendWhoseMessageNoTurnNamesFailsClosed(t *testing.T) {
	app, _ := peerTargetApp(t, surface.RuntimeClaudeHost, []sessionstream.Event{
		{Seq: 1, ItemID: "other", Version: 1, Kind: "message", Role: "peer", TurnID: "other-turn", MessageID: "other-message", Body: "hello"},
		{Seq: 2, ItemID: "answer", Version: 1, Kind: "text", Role: "assistant", TurnID: "other-turn", Body: "not yours"},
		{Seq: 3, ItemID: "done", Kind: "done", TurnID: "other-turn"},
	})
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"send", "claude:s", "hello", "--reply", "--timeout", "200ms"})
	})
	if err == nil || !strings.Contains(err.Error(), "could not find the turn that took message msg-1") || strings.Contains(output, "not yours") {
		t.Fatalf("output=%q err=%v, want a timeout that adopts no other turn", output, err)
	}
}

func TestAReplySendToAClaudeInATerminalIsRefusedBeforeSending(t *testing.T) {
	app, fake := peerTargetApp(t, surface.RuntimeClaudeCLI, nil)
	for _, mode := range []string{"--reply", "--stream"} {
		err := app.Run([]string{"send", "claude:s", "hello", mode})
		if err == nil || err.Error() != errTerminalPeerTurns.Error() || len(fake.sent) != 0 {
			t.Fatalf("mode=%s err=%v sent=%v, want the refusal and nothing sent", mode, err, fake.sent)
		}
	}
}

func TestAReplySendWhoseMessageTheHostedClaudeHoldsStopsWithTheCause(t *testing.T) {
	app, _ := peerTargetApp(t, surface.RuntimeClaudeHost, nil)
	done := make(chan error, 1)
	go func() {
		done <- app.Run([]string{"send", "claude:s", "hello", "--reply", "--timeout", "30s"})
	}()
	var intent *registry.DeliveryIntent
	testkit.Eventually(t, "the send's delivery intent", 10*time.Second, func() bool {
		intent, _ = app.Registry.DeliveryIntent(1)
		return intent != nil && intent.Status == registry.DeliveryIntentSent
	})
	if held, err := app.Registry.HoldPeerMessage(registry.PeerMessage{MsgID: intent.UUID, TargetSessionID: "s"}, "the sender's permission mode differs"); err != nil || !held {
		t.Fatalf("held=%v err=%v", held, err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "the target holds this message and started no turn: the sender's permission mode differs") {
			t.Fatalf("err=%v, want the hold and its cause", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the send kept waiting for a turn after its message was held")
	}
}
