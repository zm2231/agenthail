package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

type testSessionPageReader struct{}

func (testSessionPageReader) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	return &surface.SessionReadResult{JournalSeq: 10, Source: "journal", UnavailableReason: "", Items: []surface.TimelineItem{}, Exchanges: []surface.Exchange{}}, nil
}

type testSessionStreamReader struct {
	events []sessionstream.Event
}

func (r testSessionStreamReader) OpenSessionStream(context.Context, *surface.Session, uint64) (sessionstream.Subscription, error) {
	events := make(chan sessionstream.Event, len(r.events))
	for _, event := range r.events {
		events <- event
	}
	close(events)
	return sessionstream.Subscription{Events: events, Cancel: func() {}}, nil
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

type unavailableSessionPageReader struct{}

func (unavailableSessionPageReader) ReadSession(context.Context, *surface.Session, surface.SessionReadRequest) (*surface.SessionReadResult, error) {
	return &surface.SessionReadResult{JournalSeq: 10, Source: "journal", UnavailableReason: "provider unavailable"}, nil
}
