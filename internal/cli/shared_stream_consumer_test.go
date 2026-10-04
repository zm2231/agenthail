package cli

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

type testSessionPageReader struct{}

func (testSessionPageReader) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	return &surface.SessionReadResult{JournalSeq: 10, Source: "journal", UnavailableReason: "", Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}}, nil
}

type testSessionStreamReader struct {
	events    []sessionstream.Event
	blocked   bool
	cancelled *atomic.Int32
}

func (r testSessionStreamReader) OpenSessionStream(context.Context, *surface.Session, uint64) (sessionstream.Subscription, error) {
	var events chan sessionstream.Event
	if r.blocked {
		events = make(chan sessionstream.Event)
	} else {
		events = make(chan sessionstream.Event, len(r.events))
		for _, event := range r.events {
			events <- event
		}
		close(events)
	}
	return sessionstream.Subscription{Events: events, Cancel: func() {
		if r.cancelled != nil {
			r.cancelled.Add(1)
		}
	}}, nil
}

func TestSendStreamActiveDaemonUsesPreparedJournalAndFastReply(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{
		kind:         surface.KindCodex,
		sessions:     map[string]surface.Session{"s": session},
		caps:         surface.Capabilities{Send: true, Stream: true},
		sendResult:   &surface.SendResult{UUID: "turn-a", Accepted: true},
		streamEvents: []surface.StreamEvent{{Kind: "text", Text: "wrong-provider-reader"}, {Kind: "done"}},
	}
	app, _ := cliFixture(t, fake)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) { return testSessionPageReader{}, nil }
	app.daemonSessionStreamReader = func() (sessionStreamReader, error) {
		return testSessionStreamReader{events: []sessionstream.Event{
			{Seq: 11, ItemID: "other", Version: 1, Kind: "text", Role: "assistant", TurnID: "other-turn", Body: "unrelated"},
			{Seq: 12, ItemID: "answer", Version: 1, Kind: "text", Role: "assistant", TurnID: "turn-a", Body: "fast"},
			{Seq: 13, ItemID: "done", Kind: "done", TurnID: "turn-a"},
		}}, nil
	}
	output, err := captureStdout(t, func() error { return app.cmdSend([]string{"codex:s", "hello", "--stream"}) })
	if err != nil || output != "fast\n" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestSendStreamActiveDaemonFailsOnCancelledTerminal(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{
		kind:         surface.KindCodex,
		sessions:     map[string]surface.Session{"s": session},
		caps:         surface.Capabilities{Send: true, Stream: true},
		sendResult:   &surface.SendResult{UUID: "turn-a", Accepted: true},
		streamEvents: []surface.StreamEvent{},
	}
	app, _ := cliFixture(t, fake)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) { return testSessionPageReader{}, nil }
	app.daemonSessionStreamReader = func() (sessionStreamReader, error) {
		return testSessionStreamReader{events: []sessionstream.Event{{Seq: 11, ItemID: "cancelled", Kind: "done", Status: "cancelled", TurnID: "turn-a"}}}, nil
	}
	output, err := captureStdout(t, func() error { return app.cmdSend([]string{"codex:s", "hello", "--stream"}) })
	if err == nil || !strings.Contains(err.Error(), "did not complete successfully: cancelled") || output != "" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestSendStreamActiveDaemonFailsClosedOnSourceError(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"s": session}, caps: surface.Capabilities{Send: true, Stream: true}, sendResult: &surface.SendResult{UUID: "turn-a", Accepted: true}}
	app, _ := cliFixture(t, fake)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) {
		return unavailableSessionPageReader{}, nil
	}
	err := app.cmdSend([]string{"codex:s", "hello", "--stream"})
	if err == nil || !strings.Contains(err.Error(), "source unavailable") || len(fake.sent) != 0 {
		t.Fatalf("err=%v sent=%v", err, fake.sent)
	}
}

func TestStreamActiveDaemonTimeoutCancelsSubscription(t *testing.T) {
	session := surface.Session{ID: "s", Surface: surface.KindCodex}
	cancelled := atomic.Int32{}
	fake := &cliSurface{
		kind:     surface.KindCodex,
		sessions: map[string]surface.Session{"s": session},
		caps:     surface.Capabilities{Stream: true},
	}
	app, _ := cliFixture(t, fake)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) { return testSessionPageReader{}, nil }
	app.daemonSessionStreamReader = func() (sessionStreamReader, error) {
		return testSessionStreamReader{blocked: true, cancelled: &cancelled}, nil
	}
	started := time.Now()
	err := app.cmdStream([]string{"codex:s", "--timeout", "20ms"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("stream timeout took %s", elapsed)
	}
	if got := cancelled.Load(); got != 1 {
		t.Fatalf("cancel count=%d, want one cleanup", got)
	}
}

func TestSendReplyToNewNotionThreadNeverStealsTakenAlias(t *testing.T) {
	synthetic := surface.Session{ID: "new:launch-notes", Surface: surface.KindNotion, Name: "launch-notes", Status: surface.StatusIdle}
	fake := &cliSurface{
		kind:       surface.KindNotion,
		sessions:   map[string]surface.Session{"new:launch-notes": synthetic},
		caps:       surface.Capabilities{Send: true},
		sendResult: &surface.SendResult{UUID: "notion-thread", Accepted: true},
	}
	app, registry := cliFixture(t, fake)
	if err := registry.RegisterSession(surface.Session{ID: "existing", Surface: surface.KindNotion}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetAlias("launch-notes", "existing"); err != nil {
		t.Fatal(err)
	}
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonSessionPageReader = func() (sessionPageReader, error) { return testSessionPageReader{}, nil }
	app.daemonSessionStreamReader = func() (sessionStreamReader, error) { return testSessionStreamReader{}, nil }
	err := app.cmdSend([]string{"notion:new:launch-notes", "draft", "--reply"})
	if err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("err=%v, want alias registration failure", err)
	}
	if owner, err := registry.LookupAlias("launch-notes"); err != nil || owner != "existing" {
		t.Fatalf("alias owner=%q err=%v", owner, err)
	}
}

type unavailableSessionPageReader struct{}

func (unavailableSessionPageReader) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	return &surface.SessionReadResult{JournalSeq: 10, Source: "journal", UnavailableReason: "provider unavailable"}, nil
}
